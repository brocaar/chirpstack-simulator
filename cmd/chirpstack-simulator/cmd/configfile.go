package cmd

import (
	"os"
	"text/template"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/brocaar/chirpstack-simulator/internal/config"
)

const configTemplate = `[general]
# Log level
#
# debug=5, info=4, warning=3, error=2, fatal=1, panic=0
log_level={{ .General.LogLevel }}


# Application Server configuration.
[application_server]

  # API configuration.
  #
  # This configuration is used to automatically create the:
  #   * Device profile
  #   * Gateways
  #   * Application
  #   * Devices
  [application_server.api]

  # JWT token.
  #
  # The JWT token to connect to the ChirpStack Application Server API. This
  # token can be generated using the login API endpoint. In the near-future
  # it will be possible to generate these tokens within the web-interface:
  # https://github.com/brocaar/chirpstack-application-server/pull/421
  jwt_token="{{ .ApplicationServer.API.JWTToken }}"

  # Server.
  #
  # This must point to the external API server of the ChirpStack Application
  # Server. When the server is running on the same machine, keep this to the
  # default value.
  server="{{ .ApplicationServer.API.Server }}"

  # Insecure.
  #
  # Set this to true when the endpoint is not using TLS.
  insecure={{ .ApplicationServer.API.Insecure }}


  # MQTT integration configuration.
  #
  # This integration is used for counting the number of uplinks that are
  # published by the ChirpStack Application Server integration.
  [application_server.integration.mqtt]

  # MQTT server.
  server="{{ .ApplicationServer.Integration.MQTT.Server }}"

  # Username.
  username="{{ .ApplicationServer.Integration.MQTT.Username }}"

  # Password.
  password="{{ .ApplicationServer.Integration.MQTT.Password }}"


# Network Server configuration.
#
# This configuration is used to simulate LoRa gateways using the MQTT gateway
# backend.
[network_server]

  # MQTT gateway backend.
  [network_server.gateway.backend.mqtt]

  # MQTT server.
  server="{{ .NetworkServer.Gateway.Backend.MQTT.Server }}"

  # Username.
  username="{{ .NetworkServer.Gateway.Backend.MQTT.Username }}"

  # Password.
  password="{{ .NetworkServer.Gateway.Backend.MQTT.Password }}"


# Simulator configuration.
#
# Example:
# [[simulator]]
#
# # Service-profile ID.
# #
# # It is recommended to create a new organization with a new service-profile
# # in the ChirpStack Application Server.
# service_profile_id="1f32476e-a112-4f00-bcc7-4aab4bfefa1d"
#
# # Duration.
# #
# # This defines the duration of the simulation. If set to '0s', the simulation
# # will run until terminated. This includes the activation time.
# duration="5m"
#
# # Activation time.
# #
# # This is the time that the simulator takes to activate the devices. This
# # value must be less than the simulator duration.
# activation_time="1m"
#
#   # Device configuration.
#   [simulator.device]
#
#   # Number of devices to simulate.
#   count=1000
#
#   # Uplink interval.
#   uplink_interval="5m"
#
#   # FPort.
#   f_port=10
#
#   # Payload (HEX encoded).
#   payload="010203"
#
#   # Frequency (Hz).
#   frequency=868100000
#
#   # Bandwidth (Hz).
#   bandwidth=125000
#
#   # Spreading-factor.
#   spreading_factor=7
#
#   # Gateway configuration.
#   [simulator.gateway]
#
#   # Event topic template.
#   event_topic_template="{{ "gateway/{{ .GatewayID }}/event/{{ .Event }}" }}"
#
#   # Command topic template.
#   command_topic_template="{{ "gateway/{{ .GatewayID }}/command/{{ .Command }}" }}"
#
#   # Min number of receiving gateways.
#   min_count=3
#
#   # Max number of receiving gateways.
#   max_count=5
{{ range $index, $element := .Simulator }}
[[simulator]]
service_profile_id="{{ $element.ServiceProfileID }}"
duration="{{ $element.Duration }}"
activation_time="{{ $element.ActivationTime }}"

  [simulator.device]
  count={{ $element.Device.Count }}
  uplink_interval="{{ $element.Device.UplinkInterval }}"
  f_port="{{ $element.Device.FPort }}"
  payload="{{ $element.Device.Payload }}"
  frequency={{ $element.Device.Frequency }}
  bandwidth={{ $element.Device.Bandwidth }}
  spreading_factor={{ $element.Device.SpreadingFactor }}

  [simulator.gateway]
  min_count={{ $element.Gateway.MinCount }}
  max_count={{ $element.Gateway.MaxCount }}
  event_topic_template="{{ $element.Gateway.EventTopicTemplate }}"
  command_topic_template="{{ $element.Gateway.CommandTopicTemplate }}"
{{ end }}

# Prometheus metrics configuration.
#
# Using Prometheus (and Grafana), it is possible to visualize various
# simulation metrics like:
#   * Join-Requests sent
#   * Join-Accepts received
#   * Uplinks sent (by the devices)
#   * Uplinks sent (by the gateways)
#   * Uplinks sent (by the ChirpStack Application Server MQTT integration)
[prometheus]

# IP:port to bind the Prometheus endpoint to.
#
# Metrics can be retrieved from /metrics.
bind="{{ .Prometheus.Bind }}"
`

var configCmd = &cobra.Command{
	Use:   "configfile",
	Short: "Print the ChirpStack Network Server configuration file",
	RunE: func(cmd *cobra.Command, args []string) error {
		t := template.Must(template.New("config").Parse(configTemplate))
		err := t.Execute(os.Stdout, &config.C)
		if err != nil {
			return errors.Wrap(err, "execute config template error")
		}
		return nil
	},
}
