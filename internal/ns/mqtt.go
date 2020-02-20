package ns

import (
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"

	"github.com/brocaar/chirpstack-simulator/internal/config"
)

var mqttClient mqtt.Client

// Setup configures the NS MQTT gateway backend.
func Setup(c config.Config) error {
	conf := c.NetworkServer.Gateway.Backend.MQTT

	opts := mqtt.NewClientOptions()
	opts.AddBroker(conf.Server)
	opts.SetUsername(conf.Username)
	opts.SetPassword(conf.Password)
	opts.SetCleanSession(true)
	opts.SetAutoReconnect(true)

	log.WithFields(log.Fields{
		"server": conf.Server,
	}).Info("ns: connecting to mqtt broker")

	mqttClient = mqtt.NewClient(opts)
	if token := mqttClient.Connect(); token.Wait() && token.Error() != nil {
		return errors.Wrap(token.Error(), "mqtt client connect error")
	}

	return nil
}

func Client() mqtt.Client {
	return mqttClient
}
