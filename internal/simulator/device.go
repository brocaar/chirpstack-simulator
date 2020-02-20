package simulator

import (
	"context"
	"math/rand"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/brocaar/chirpstack-api/go/v3/common"
	"github.com/brocaar/chirpstack-api/go/v3/gw"
	"github.com/brocaar/lorawan"
	"github.com/pkg/errors"
)

type deviceState int

const (
	deviceStateOTAA deviceState = iota
	deviceStateActivated
)

// Device defines a device.
type Device struct {
	sync.RWMutex

	ctx context.Context
	wg  *sync.WaitGroup

	devEUI         lorawan.EUI64
	joinEUI        lorawan.EUI64
	appKey         lorawan.AES128Key
	uplinkInterval time.Duration
	payload        []byte
	fPort          uint8

	devAddr  lorawan.DevAddr
	devNonce lorawan.DevNonce
	fCntUp   uint32
	fCntDown uint32
	appSKey  lorawan.AES128Key
	nwkSKey  lorawan.AES128Key

	state deviceState

	downlinkFrames chan gw.DownlinkFrame
	gateways       []*Gateway
}

// NewDevice creates a new device.
func NewDevice(ctx context.Context, wg *sync.WaitGroup, devEUI lorawan.EUI64, appKey lorawan.AES128Key, interval time.Duration, fPort uint8, payload []byte, gateways []*Gateway) (*Device, error) {
	log.WithFields(log.Fields{
		"dev_eui": devEUI,
	}).Info("simulator: new otaa device")

	d := &Device{
		ctx:            ctx,
		wg:             wg,
		devEUI:         devEUI,
		appKey:         appKey,
		uplinkInterval: interval,
		payload:        payload,
		fPort:          fPort,

		gateways:       gateways,
		downlinkFrames: make(chan gw.DownlinkFrame, 100),
		state:          deviceStateOTAA,
	}

	for i := range d.gateways {
		d.gateways[i].AddDevice(d.devEUI, d.downlinkFrames)
	}

	wg.Add(2)

	go d.uplinkLoop()
	go d.downlinkLoop()

	return d, nil
}

func (d *Device) uplinkLoop() {
	defer d.wg.Done()

	var cancelled bool
	go func() {
		<-d.ctx.Done()
		cancelled = true
	}()

	time.Sleep(time.Duration(rand.Intn(int(d.uplinkInterval))))

	for !cancelled {
		switch d.getState() {
		case deviceStateOTAA:
			d.joinRequest()
			time.Sleep(6 * time.Second)
		case deviceStateActivated:
			d.unconfirmedDataUp()
			time.Sleep(d.uplinkInterval)
		}
	}
}

func (d *Device) downlinkLoop() {
	defer d.wg.Done()

	for {
		select {
		case <-d.ctx.Done():
			return

		case pl := <-d.downlinkFrames:
			err := func() error {
				var phy lorawan.PHYPayload

				if err := phy.UnmarshalBinary(pl.PhyPayload); err != nil {
					return errors.Wrap(err, "unmarshal phypayload error")
				}

				switch phy.MHDR.MType {
				case lorawan.JoinAccept:
					return d.joinAccept(phy)
				}

				return nil
			}()

			if err != nil {
				log.WithError(err).Error("simulator: handle downlink frame error")
			}
		}
	}
}

func (d *Device) joinRequest() {
	log.WithFields(log.Fields{
		"dev_eui": d.devEUI,
	}).Debug("simulator: send OTAA request")

	phy := lorawan.PHYPayload{
		MHDR: lorawan.MHDR{
			MType: lorawan.JoinRequest,
			Major: lorawan.LoRaWANR1,
		},
		MACPayload: &lorawan.JoinRequestPayload{
			DevEUI:   d.devEUI,
			JoinEUI:  d.joinEUI,
			DevNonce: d.getDevNonce(),
		},
	}

	if err := phy.SetUplinkJoinMIC(d.appKey); err != nil {
		log.WithError(err).Error("simulator: set uplink join mic error")
		return
	}

	d.sendUplink(phy)

	deviceJoinRequestCounter().Inc()
}

func (d *Device) unconfirmedDataUp() {
	log.WithFields(log.Fields{
		"dev_eui":  d.devEUI,
		"dev_addr": d.devAddr,
	}).Debug("simulator: send unconfirmed data up")

	phy := lorawan.PHYPayload{
		MHDR: lorawan.MHDR{
			MType: lorawan.UnconfirmedDataUp,
			Major: lorawan.LoRaWANR1,
		},
		MACPayload: &lorawan.MACPayload{
			FHDR: lorawan.FHDR{
				DevAddr: d.devAddr,
				FCnt:    d.fCntUp,
				FCtrl: lorawan.FCtrl{
					ADR: false,
				},
			},
			FPort: &d.fPort,
			FRMPayload: []lorawan.Payload{
				&lorawan.DataPayload{
					Bytes: d.payload,
				},
			},
		},
	}

	if err := phy.EncryptFRMPayload(d.appSKey); err != nil {
		log.WithError(err).Error("simulator: encrypt FRMPayload error")
		return
	}

	if err := phy.SetUplinkDataMIC(lorawan.LoRaWAN1_0, 0, 0, 0, d.nwkSKey, d.nwkSKey); err != nil {
		log.WithError(err).Error("simulator: set uplink data mic error")
		return
	}

	d.fCntUp++

	d.sendUplink(phy)

	deviceUplinkCounter().Inc()
}

func (d *Device) joinAccept(phy lorawan.PHYPayload) error {
	err := phy.DecryptJoinAcceptPayload(d.appKey)
	if err != nil {
		return errors.Wrap(err, "decrypt join-accept payload error")
	}

	ok, err := phy.ValidateDownlinkJoinMIC(lorawan.JoinRequestType, d.joinEUI, d.devNonce, d.appKey)
	if err != nil {
		log.WithFields(log.Fields{
			"dev_eui": d.devEUI,
		}).Debug("simulator: invalid join-accept MIC")
		return nil
	}
	if !ok {
		log.WithFields(log.Fields{
			"dev_eui": d.devEUI,
		}).Debug("simulator: invalid join-accept MIC")
		return nil
	}

	jaPL, ok := phy.MACPayload.(*lorawan.JoinAcceptPayload)
	if !ok {
		return errors.New("expected *lorawan.JoinAcceptPayload")
	}

	d.appSKey, err = getAppSKey(jaPL.DLSettings.OptNeg, d.appKey, jaPL.HomeNetID, d.joinEUI, jaPL.JoinNonce, d.devNonce)
	if err != nil {
		return errors.Wrap(err, "get AppSKey error")
	}

	d.nwkSKey, err = getFNwkSIntKey(jaPL.DLSettings.OptNeg, d.appKey, jaPL.HomeNetID, d.joinEUI, jaPL.JoinNonce, d.devNonce)
	if err != nil {
		return errors.Wrap(err, "get NwkSKey error")
	}

	d.devAddr = jaPL.DevAddr

	log.WithFields(log.Fields{
		"dev_eui":  d.devEUI,
		"dev_addr": d.devAddr,
	}).Info("simulator: device OTAA activated")

	d.setState(deviceStateActivated)
	deviceJoinAcceptCounter().Inc()

	return nil
}

func (d *Device) getDevNonce() lorawan.DevNonce {
	d.devNonce++
	return d.devNonce
}

func (d *Device) getState() deviceState {
	d.RLock()
	defer d.RUnlock()

	return d.state
}

func (d *Device) setState(s deviceState) {
	d.Lock()
	d.Unlock()

	d.state = s
}

func (d *Device) sendUplink(phy lorawan.PHYPayload) error {
	b, err := phy.MarshalBinary()
	if err != nil {
		return errors.Wrap(err, "marshal phypayload error")
	}

	txInfo := gw.UplinkTXInfo{
		Frequency:  868100000,
		Modulation: common.Modulation_LORA,
		ModulationInfo: &gw.UplinkTXInfo_LoraModulationInfo{
			LoraModulationInfo: &gw.LoRaModulationInfo{
				Bandwidth:       125,
				SpreadingFactor: 7,
				CodeRate:        "3/4",
			},
		},
	}

	pl := gw.UplinkFrame{
		PhyPayload: b,
		TxInfo:     &txInfo,
	}

	for i := range d.gateways {
		if err := d.gateways[i].SendUplinkFrame(pl); err != nil {
			log.WithError(err).WithFields(log.Fields{
				"dev_eui": d.devEUI,
			}).Error("simulator: send uplink frame error")
		}
	}

	return nil
}
