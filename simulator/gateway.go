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

// Gateway defines a simulated LoRa gateway.
type Gateway struct {
	mqtt      mqtt.Client
	gatewayID lorawan.EUI64

	deviceMux sync.RWMutex
	devices   map[lorawan.EUI64]chan gw.DownlinkFrame
}

// WithMQTTClient sets the MQTT client for the gateway.
func WithMQTTClient(client mqtt.Client) func(*Gateway) {
	return func(g *Gateway) {
		g.mqtt = client
	}
}

// WithGatewayID sets the gateway ID.
func WithGatewayID(gatewayID lorawan.EUI64) func(*Gateway) {
	return func(g *Gateway) {
		g.gatewayID = gatewayID
	}
}

// NewGateway creates a new gateway, using the given MQTT client for sending
// and receiving.
func NewGateway(opts ...func(*Gateway)) (*Gateway, error) {
	gw := &Gateway{
		devices: make(map[lorawan.EUI64]chan gw.DownlinkFrame),
	}

	for _, o := range opts {
		o(gw)
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

// AddDevice adds the given device to the 'coverage' of the gateway.
// This means that any downlink sent to the gateway will be forwarded to added
// devices (which will each validate the DevAddr and MIC).
func (g *Gateway) AddDevice(devEUI lorawan.EUI64, c chan gw.DownlinkFrame) {
	g.deviceMux.Lock()
	defer g.deviceMux.Unlock()

	log.WithFields(log.Fields{
		"dev_eui":    devEUI,
		"gateway_id": g.gatewayID,
	}).Info("simulator: add device to gateway")

	g.devices[devEUI] = c
}

// RemoveDevice removes the given device from the gateway 'coverage'.
func (g *Gateway) RemoveDevice(devEUI lorawan.EUI64) {
	g.deviceMux.Lock()
	defer g.deviceMux.Unlock()

	delete(g.devices, devEUI)
}

func (g *Gateway) getUplinkTopic() string {
	return fmt.Sprintf("gateway/%s/event/up", g.gatewayID)
}

func (g *Gateway) getDownlinkTopic() string {
	return fmt.Sprintf("gateway/%s/command/down", g.gatewayID)
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
}
