package simulator

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"io/ioutil"
	"sync"
	"text/template"
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

	eventTopicTemplate   *template.Template
	commandTopicTemplate *template.Template
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

// WithMQTTCertificates initializes a new MQTT client with the given CA and
// client-certificate files.
func WithMQTTCertificates(server, caCert, tlsCert, tlsKey string) GatewayOption {
	return func(g *Gateway) error {
		tlsConfig := &tls.Config{}

		if caCert != "" {
			b, err := ioutil.ReadFile(caCert)
			if err != nil {
				return errors.Wrap(err, "read ca certificate error")
			}

			certpool := x509.NewCertPool()
			certpool.AppendCertsFromPEM(b)
			tlsConfig.RootCAs = certpool
		}

		if tlsCert != "" && tlsKey != "" {
			kp, err := tls.LoadX509KeyPair(tlsCert, tlsKey)
			if err != nil {
				return errors.Wrap(err, "read tls key-pair error")
			}

			tlsConfig.Certificates = []tls.Certificate{kp}
		}

		opts := mqtt.NewClientOptions()
		opts.AddBroker(server)
		opts.SetCleanSession(true)
		opts.SetAutoReconnect(true)
		opts.SetTLSConfig(tlsConfig)

		log.WithFields(log.Fields{
			"ca_cert":  caCert,
			"tls_cert": tlsCert,
			"tls_key":  tlsKey,
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

// WithEventTopicTemplate sets the event (gw > ns) topic template.
// Example: 'gateway/{{ .GatewayID }}/event/{{ .Event }}'
func WithEventTopicTemplate(tt string) GatewayOption {
	return func(g *Gateway) error {
		var err error
		g.eventTopicTemplate, err = template.New("event").Parse(tt)
		if err != nil {
			return errors.Wrap(err, "parse event topic template error")
		}

		return nil
	}
}

// WithCommandTopicTemplate sets the command (ns > gw) topic template.
// Example: 'gateway/{{ .GatewayID }}/command/{{ .Command }}'
func WithCommandTopicTemplate(ct string) GatewayOption {
	return func(g *Gateway) error {
		var err error
		g.commandTopicTemplate, err = template.New("command").Parse(ct)
		if err != nil {
			return errors.Wrap(err, "parse command topic template error")
		}

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

	downlinkTopic := gw.getCommandTopic("down")

	log.WithFields(log.Fields{
		"gateway_id": gw.gatewayID,
		"topic":      downlinkTopic,
	}).Info("simulator: subscribing to gateway mqtt topic")
	for {
		if token := gw.mqtt.Subscribe(downlinkTopic, 0, gw.downlinkEventHandler); token.Wait() && token.Error() != nil {
			log.WithError(token.Error()).WithFields(log.Fields{
				"gateway_id": gw.gatewayID,
				"topic":      downlinkTopic,
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

	uplinkTopic := g.getEventTopic("up")

	log.WithFields(log.Fields{
		"gateway_id": g.gatewayID,
		"topic":      uplinkTopic,
	}).Debug("simulator: publish uplink frame")

	if token := g.mqtt.Publish(uplinkTopic, 0, false, b); token.Wait() && token.Error() != nil {
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

	ackTopic := g.getEventTopic("ack")

	log.WithFields(log.Fields{
		"gateway_id": g.gatewayID,
		"topic":      ackTopic,
		"error":      pl.Error,
	}).Debug("simulator: publish downlink tx ack")

	if token := g.mqtt.Publish(ackTopic, 0, false, b); token.Wait() && token.Error() != nil {
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

func (g *Gateway) getEventTopic(event string) string {
	topic := bytes.NewBuffer(nil)

	err := g.eventTopicTemplate.Execute(topic, struct {
		GatewayID lorawan.EUI64
		Event     string
	}{g.gatewayID, event})
	if err != nil {
		log.WithError(err).Fatal("execute event topic template error")
	}

	return topic.String()
}

func (g *Gateway) getCommandTopic(command string) string {
	topic := bytes.NewBuffer(nil)

	err := g.commandTopicTemplate.Execute(topic, struct {
		GatewayID lorawan.EUI64
		Command   string
	}{g.gatewayID, command})
	if err != nil {
		log.WithError(err).Fatal("execute command topic template error")
	}

	return topic.String()
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
