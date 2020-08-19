package main

import (
	"context"
	"encoding/hex"
	"sync"
	"time"

	"github.com/brocaar/chirpstack-api/go/v3/common"
	"github.com/brocaar/chirpstack-api/go/v3/gw"
	"github.com/brocaar/chirpstack-simulator/simulator"
	"github.com/brocaar/lorawan"
	log "github.com/sirupsen/logrus"
)

// This example simulates an OTAA activation and the sending of a single uplink
// frame, after which the simulation terminates.
func main() {
	gatewayID := lorawan.EUI64{1, 1, 1, 1, 1, 1, 1, 1}
	devEUI := lorawan.EUI64{2, 1, 1, 1, 1, 1, 1, 1}
	appKey := lorawan.AES128Key{3, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}

	var wg sync.WaitGroup
	ctx := context.Background()

	sgw, err := simulator.NewGateway(
		simulator.WithMQTTCredentials("localhost:1883", "", ""),
		simulator.WithGatewayID(gatewayID),
		simulator.WithEventTopicTemplate("gateway/{{ .GatewayID }}/event/{{ .Event }}"),
		simulator.WithCommandTopicTemplate("gateway/{{ .GatewayID }}/command/{{ .Command }}"),
	)
	if err != nil {
		panic(err)
	}

	_, err = simulator.NewDevice(ctx, &wg,
		simulator.WithDevEUI(devEUI),
		simulator.WithAppKey(appKey),
		simulator.WithRandomDevNonce(),
		simulator.WithUplinkInterval(time.Second),
		simulator.WithUplinkCount(1),
		simulator.WithUplinkPayload(true, 10, []byte{1, 2, 3}),
		simulator.WithUplinkTXInfo(gw.UplinkTXInfo{
			Frequency:  868100000,
			Modulation: common.Modulation_LORA,
			ModulationInfo: &gw.UplinkTXInfo_LoraModulationInfo{
				LoraModulationInfo: &gw.LoRaModulationInfo{
					Bandwidth:       125,
					SpreadingFactor: 7,
					CodeRate:        "3/4",
				},
			},
		}),
		simulator.WithGateways([]*simulator.Gateway{sgw}),
		simulator.WithDownlinkHandlerFunc(func(conf, ack bool, fCntDown uint32, fPort uint8, data []byte) error {
			log.WithFields(log.Fields{
				"ack":       ack,
				"fcnt_down": fCntDown,
				"f_port":    fPort,
				"data":      hex.EncodeToString(data),
			}).Info("WithDownlinkHandlerFunc triggered")

			return nil
		}),
	)
	if err != nil {
		panic(err)
	}

	wg.Wait()
}
