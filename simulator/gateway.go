package simulator

import (
	"fmt"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/gofrs/uuid"
	"github.com/golang/protobuf/proto"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"

	"github.com/brocaar/chirpstack-api/go/v3/gw"
	"github.com/brocaar/lorawan"
)

// GatewayOption is the interface for a gateway option.
type GatewayOption func(*Gateway) error

// Gateway defines a simulated LoRa gateway.
type Gateway struct {
	mqtt      mqtt.Client
	gatewayID lorawan.EUI64

	deviceMux sync.RWMutex
	devices   map[lorawan.EUI64]chan gw.DownlinkFrame

	downlinkTxNAckRate int
	downlinkTxCounter  int
	downlinkTxAckDelay time.Duration
}

// WithMQTTClient sets the MQTT client for the gateway.
func WithMQTTClient(client mqtt.Client) GatewayOption {
	return func(g *Gateway) error {
		g.mqtt = client
		return nil
	}
}

// WithMQTTCredentials initializes a new MQTT client with the given credentials.
func WithMQTTCredentials(server, username, password string) GatewayOption {
	return func(g *Gateway) error {
		opts := mqtt.NewClientOptions()
		opts.AddBroker(server)
		opts.SetUsername(username)
		opts.SetPassword(password)
		opts.SetCleanSession(true)
		opts.SetAutoReconnect(true)

		log.WithFields(log.Fields{
			"server": server,
		}).Info("simulator: connecting to mqtt broker")

		client := mqtt.NewClient(opts)
		if token := client.Connect(); token.Wait() && token.Error() != nil {
			return errors.Wrap(token.Error(), "mqtt client connect error")
		}

		g.mqtt = client

		return nil
	}
}

// WithGatewayID sets the gateway ID.
func WithGatewayID(gatewayID lorawan.EUI64) GatewayOption {
	return func(g *Gateway) error {
		g.gatewayID = gatewayID
		return nil
	}
}

// WithDownlinkTxNackRate sets the rate in which Tx NAck messages are sent.
// Setting this to:
//   0: always ACK
//   1: NAck every message
//   2: NAck every other message
//   3: NAck every third message
//   ...
func WithDownlinkTxNackRate(rate int) GatewayOption {
	return func(g *Gateway) error {
		g.downlinkTxNAckRate = rate
		return nil
	}
}

// WithDownlinkTxAckDelay sets the delay in which the Tx Ack is returned.
func WithDownlinkTxAckDelay(d time.Duration) GatewayOption {
	return func(g *Gateway) error {
		g.downlinkTxAckDelay = d
		return nil
	}
}

// NewGateway creates a new gateway, using the given MQTT client for sending
// and receiving.
func NewGateway(opts ...GatewayOption) (*Gateway, error) {
	gw := &Gateway{
		devices: make(map[lorawan.EUI64]chan gw.DownlinkFrame),
	}

	for _, o := range opts {
		if err := o(gw); err != nil {
			return nil, err
		}
	}

	log.WithFields(log.Fields{
		"gateway_id": gw.gatewayID,
		"topic":      gw.getDownlinkTopic(),
	}).Info("simulator: subscribing to gateway mqtt topic")
	for {
		if token := gw.mqtt.Subscribe(gw.getDownlinkTopic(), 0, gw.downlinkEventHandler); token.Wait() && token.Error() != nil {
			log.WithError(token.Error()).WithFields(log.Fields{
				"gateway_id": gw.gatewayID,
				"topic":      gw.getDownlinkTopic(),
			}).Error("simulator: subscribe to mqtt topic error")
			time.Sleep(time.Second * 2)
		} else {
			break
		}
	}

	return gw, nil
}

// SendUplinkFrame sends the given uplink frame.
func (g *Gateway) SendUplinkFrame(pl gw.UplinkFrame) error {
	uplinkID, err := uuid.NewV4()
	if err != nil {
		return errors.Wrap(err, "new uuid error")
	}

	pl.RxInfo = &gw.UplinkRXInfo{
		GatewayId: g.gatewayID[:],
		Rssi:      50,
		LoraSnr:   5.5,
		Context:   []byte{0x01, 0x02, 0x03, 0x04},
		UplinkId:  uplinkID[:],
	}

	b, err := proto.Marshal(&pl)
	if err != nil {
		return errors.Wrap(err, "send uplink frame error")
	}

	log.WithFields(log.Fields{
		"gateway_id": g.gatewayID,
		"topic":      g.getUplinkTopic(),
	}).Debug("simulator: publish uplink frame")

	if token := g.mqtt.Publish(g.getUplinkTopic(), 0, false, b); token.Wait() && token.Error() != nil {
		return errors.Wrap(err, "simulator: publish uplink frame error")
	}

	gatewayUplinkCounter().Inc()

	return nil
}

// sendDownlinkTxAck sends the given downlink Ack.
func (g *Gateway) sendDownlinkTxAck(pl gw.DownlinkTXAck) error {
	b, err := proto.Marshal(&pl)
	if err != nil {
		return errors.Wrap(err, "send tx ack error")
	}

	log.WithFields(log.Fields{
		"gateway_id": g.gatewayID,
		"topic":      g.getDownlinkTxAckTopic(),
		"error":      pl.Error,
	}).Debug("simulator: publish downlink tx ack")

	if token := g.mqtt.Publish(g.getDownlinkTxAckTopic(), 0, false, b); token.Wait() && token.Error() != nil {
		return errors.Wrap(err, "simulator: publish downlink tx ack error")
	}

	return nil
}

// addDevice adds the given device to the 'coverage' of the gateway.
// This means that any downlink sent to the gateway will be forwarded to added
// devices (which will each validate the DevAddr and MIC).
func (g *Gateway) addDevice(devEUI lorawan.EUI64, c chan gw.DownlinkFrame) {
	g.deviceMux.Lock()
	defer g.deviceMux.Unlock()

	log.WithFields(log.Fields{
		"dev_eui":    devEUI,
		"gateway_id": g.gatewayID,
	}).Info("simulator: add device to gateway")

	g.devices[devEUI] = c
}

func (g *Gateway) getUplinkTopic() string {
	return fmt.Sprintf("gateway/%s/event/up", g.gatewayID)
}

func (g *Gateway) getDownlinkTopic() string {
	return fmt.Sprintf("gateway/%s/command/down", g.gatewayID)
}

func (g *Gateway) getDownlinkTxAckTopic() string {
	return fmt.Sprintf("gateway/%s/event/ack", g.gatewayID)
}

func (g *Gateway) downlinkEventHandler(c mqtt.Client, msg mqtt.Message) {
	g.deviceMux.RLock()
	defer g.deviceMux.RUnlock()

	log.WithFields(log.Fields{
		"gateway_id": g.gatewayID,
		"topic":      msg.Topic(),
	}).Debug("simulator: downlink command received")

	gatewayDownlinkCounter().Inc()

	var pl gw.DownlinkFrame
	if err := proto.Unmarshal(msg.Payload(), &pl); err != nil {
		log.WithError(err).Error("simulator: unmarshal downlink command error")
	}

	for devEUI, downChan := range g.devices {
		log.WithFields(log.Fields{
			"dev_eui":    devEUI,
			"gateway_id": g.gatewayID,
		}).Debug("simulator: forwarding downlink to device")
		downChan <- pl
	}

	time.Sleep(g.downlinkTxAckDelay)

	ackError := ""
	g.downlinkTxCounter++
	if g.downlinkTxCounter == g.downlinkTxNAckRate {
		ackError = "COLLISION_PACKET"
		g.downlinkTxCounter = 0
	}

	if err := g.sendDownlinkTxAck(gw.DownlinkTXAck{
		GatewayId:  g.gatewayID[:],
		Token:      pl.Token,
		DownlinkId: pl.DownlinkId,
		Error:      ackError,
	}); err != nil {
		log.WithError(err).WithFields(log.Fields{
			"gateway_id": g.gatewayID,
		}).Error("simulator: send downlink tx ack error")
	}
}
