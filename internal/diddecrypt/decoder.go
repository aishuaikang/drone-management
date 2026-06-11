package diddecrypt

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"dr600ab-net/internal/model"
)

const (
	DefaultKeyPacketTTL = 10 * time.Minute
	FallbackModel       = "DJI-Drone"
	O4Source            = "dji_O:4"
)

type PacketType int

const (
	PacketUnknown PacketType = iota
	PacketDirect
	PacketKey
	PacketDynamic
)

type Status string

const (
	StatusInvalid          Status = "invalid"
	StatusUnsupported      Status = "unsupported"
	StatusDecoded          Status = "decoded"
	StatusUncracked        Status = "uncracked"
	StatusKeyCached        Status = "key_cached"
	StatusKeyAlreadyCached Status = "key_already_cached"
	StatusPendingKey       Status = "pending_key"
)

type Request struct {
	EncryptedID  string
	EncryptedHex string
	DecryptedHex string
	DeviceSN     string
	PacketType   PacketType
}

type DecryptResult struct {
	Msg      string  `json:"msg,omitempty"`
	Note     string  `json:"note,omitempty"`
	SN       string  `json:"sn,omitempty"`
	Model    string  `json:"model,omitempty"`
	Lon      float64 `json:"lon,omitempty"`
	Lat      float64 `json:"lat,omitempty"`
	Alt      float64 `json:"alt,omitempty"`
	Height   float64 `json:"height,omitempty"`
	X        float64 `json:"x,omitempty"`
	Y        float64 `json:"y,omitempty"`
	Z        float64 `json:"z,omitempty"`
	PilotLon float64 `json:"pilot_lon,omitempty"`
	PilotLat float64 `json:"pilot_lat,omitempty"`
	HomeLon  float64 `json:"home_lon,omitempty"`
	HomeLat  float64 `json:"home_lat,omitempty"`
	GPSTime  string  `json:"gps_time,omitempty"`
	SeqNum   int     `json:"seq_num,omitempty"`
	Type     int     `json:"type,omitempty"`
	UUID     string  `json:"uuid,omitempty"`
	Yaw      float64 `json:"yaw,omitempty"`
}

type KeyResult struct {
	Msg     string
	Note    string
	Success bool
	Err     error
	Data    DecryptResult
}

type Client interface {
	Decrypt(ctx context.Context, req Request) (DecryptResult, error)
	SendKeyPacket(ctx context.Context, req Request) KeyResult
}

type Packet struct {
	Device      string
	EncryptedID string
	Freq        float64
	RSSI        float64
	Bytes       string
}

type Options struct {
	KeyTTL                   time.Duration
	EmitUncrackedTarget      bool
	RequireDecodedCoordinate bool
	Now                      func() time.Time
}

type Decoder struct {
	client Client
	opts   Options
	mu     sync.Mutex
	keys   map[string]keyPacket
}

type Output struct {
	Target    model.ScreenPositionTarget
	Status    Status
	HasTarget bool
	Err       error
}

type keyPacket struct {
	EncryptedID string
	Hex         string
	Device      string
	Frequency   float64
	RSSI        float64
	CachedAt    time.Time
}

func NewDecoder(client Client, opts Options) *Decoder {
	if opts.KeyTTL <= 0 {
		opts.KeyTTL = DefaultKeyPacketTTL
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Decoder{client: client, opts: opts, keys: make(map[string]keyPacket)}
}

func (d *Decoder) Decode(ctx context.Context, packet Packet, deviceSN string, receivedAt time.Time) Output {
	rawHex := strings.ToLower(strings.TrimSpace(packet.Bytes))
	encryptedHex, decryptedHex, ok := NormalizePacketHex(rawHex)
	if !ok {
		return Output{Status: StatusInvalid, Err: fmt.Errorf("invalid O3/O4 packet hex")}
	}
	return d.decodeHexPair(ctx, packet, encryptedHex, decryptedHex, deviceSN, receivedAt)
}

func (d *Decoder) decodeHexPair(
	ctx context.Context,
	packet Packet,
	encryptedHex string,
	decryptedHex string,
	deviceSN string,
	receivedAt time.Time,
) Output {
	if d == nil || d.client == nil {
		return Output{Status: StatusInvalid, Err: fmt.Errorf("nil DID decrypt decoder or client")}
	}
	encryptedID := strings.ToLower(strings.TrimSpace(packet.EncryptedID))
	packetType := PacketTypeFromHex(decryptedHex)
	if encryptedID == "" || packetType == PacketUnknown {
		return Output{Status: StatusUnsupported}
	}
	sn := strings.TrimSpace(deviceSN)
	if sn == "" {
		sn = strings.TrimSpace(packet.Device)
	}
	if sn == "" {
		return Output{Status: StatusInvalid, Err: fmt.Errorf("device SN is empty")}
	}
	req := Request{
		EncryptedID:  encryptedID,
		EncryptedHex: encryptedHex,
		DecryptedHex: decryptedHex,
		DeviceSN:     sn,
		PacketType:   packetType,
	}

	switch packetType {
	case PacketDirect:
		result, err := d.client.Decrypt(ctx, req)
		if err != nil {
			return d.uncrackedOutput(packet, receivedAt, StatusUncracked, err)
		}
		return d.decodedOutput(packet, result, receivedAt)

	case PacketKey:
		if d.getKeyPacket(encryptedID) != nil {
			return Output{Status: StatusKeyAlreadyCached}
		}
		result := d.client.SendKeyPacket(ctx, req)
		if result.Success {
			d.cacheKeyPacket(encryptedID, decryptedHex, packet)
			return Output{Status: StatusKeyCached}
		}
		if result.Err != nil {
			return Output{Status: StatusInvalid, Err: result.Err}
		}
		return Output{Status: StatusInvalid, Err: fmt.Errorf("key packet rejected: %s", result.Msg)}

	case PacketDynamic:
		if d.getKeyPacket(encryptedID) == nil {
			return d.uncrackedOutput(packet, receivedAt, StatusPendingKey, nil)
		}
		result, err := d.client.Decrypt(ctx, req)
		if err != nil {
			return d.uncrackedOutput(packet, receivedAt, StatusUncracked, err)
		}
		return d.decodedOutput(packet, result, receivedAt)

	default:
		return Output{Status: StatusUnsupported}
	}
}

func PacketTypeFromHex(hexStr string) PacketType {
	if len(hexStr) < 2 {
		return PacketUnknown
	}
	switch strings.ToLower(hexStr[:2]) {
	case "6d":
		return PacketDirect
	case "aa", "a3":
		return PacketKey
	case "87", "80":
		return PacketDynamic
	default:
		return PacketUnknown
	}
}

func (d *Decoder) decodedOutput(packet Packet, result DecryptResult, receivedAt time.Time) Output {
	target := TargetFromDecryptResult(packet, result, receivedAt, true)
	if d.opts.RequireDecodedCoordinate && target.Drone == nil && target.Pilot == nil && target.Home == nil {
		return Output{Status: StatusDecoded}
	}
	return Output{Target: target, Status: StatusDecoded, HasTarget: true}
}

func (d *Decoder) uncrackedOutput(packet Packet, receivedAt time.Time, status Status, err error) Output {
	if !d.opts.EmitUncrackedTarget {
		return Output{Status: status, Err: err}
	}
	return Output{
		Target:    TargetFromDecryptResult(packet, DecryptResult{Model: FallbackModel}, receivedAt, false),
		Status:    status,
		HasTarget: true,
		Err:       err,
	}
}

func TargetFromDecryptResult(
	packet Packet,
	result DecryptResult,
	receivedAt time.Time,
	cracked bool,
) model.ScreenPositionTarget {
	serial := cleanString(result.SN)
	if serial == "" {
		serial = strings.TrimSpace(packet.EncryptedID)
	}
	modelName := strings.TrimSpace(result.Model)
	if modelName == "" {
		modelName = FallbackModel
	}
	if receivedAt.IsZero() {
		receivedAt = time.Now()
	}
	speed := calculateFlightSpeed(result.X, result.Y, result.Z)
	return model.ScreenPositionTarget{
		CorrelationID:    correlationID(packet.EncryptedID),
		Serial:           serial,
		Model:            modelName,
		Source:           O4Source,
		Frequency:        packet.Freq,
		RSSI:             packet.RSSI,
		Device:           strings.TrimSpace(packet.Device),
		Drone:            pointFromLatLng(result.Lat, result.Lon),
		Pilot:            pointFromLatLng(result.PilotLat, result.PilotLon),
		Home:             pointFromLatLng(result.HomeLat, result.HomeLon),
		Height:           nonZeroFloatPtr(result.Height),
		Altitude:         nonZeroFloatPtr(result.Alt),
		Speed:            nonZeroFloatPtr(speed),
		TrajectorySpeed:  float64Ptr(speed),
		TrajectoryHeight: float64Ptr(result.Height),
		Cracked:          cracked,
		FirstSeen:        receivedAt,
		LastSeen:         receivedAt,
		LastRecord: model.ScreenPositionLastRecord{
			Type:       O4Source,
			ReceivedAt: receivedAt,
			Device:     strings.TrimSpace(packet.Device),
			Serial:     serial,
			Model:      modelName,
			Frequency:  packet.Freq,
			RSSI:       packet.RSSI,
			Cracked:    cracked,
		},
	}
}

func (d *Decoder) cacheKeyPacket(encryptedID, hexStr string, packet Packet) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.keys[encryptedID] = keyPacket{
		EncryptedID: encryptedID,
		Hex:         hexStr,
		Device:      packet.Device,
		Frequency:   packet.Freq,
		RSSI:        packet.RSSI,
		CachedAt:    d.opts.Now(),
	}
}

func (d *Decoder) getKeyPacket(encryptedID string) *keyPacket {
	d.mu.Lock()
	defer d.mu.Unlock()
	packet, ok := d.keys[encryptedID]
	if !ok {
		return nil
	}
	if d.opts.Now().Sub(packet.CachedAt) > d.opts.KeyTTL {
		delete(d.keys, encryptedID)
		return nil
	}
	return &packet
}

func correlationID(encryptedID string) string {
	encryptedID = strings.ToLower(strings.TrimSpace(encryptedID))
	if encryptedID == "" {
		return ""
	}
	return O4Source + ":" + encryptedID
}

func pointFromLatLng(lat, lng float64) *model.ScreenPositionPoint {
	if !validCoordinate(lat, lng) {
		return nil
	}
	return &model.ScreenPositionPoint{Latitude: lat, Longitude: lng}
}

func validCoordinate(lat, lng float64) bool {
	return !math.IsNaN(lat) &&
		!math.IsInf(lat, 0) &&
		!math.IsNaN(lng) &&
		!math.IsInf(lng, 0) &&
		lat >= -90 &&
		lat <= 90 &&
		lng >= -180 &&
		lng <= 180
}

func nonZeroFloatPtr(value float64) *float64 {
	if value == 0 {
		return nil
	}
	return &value
}

func float64Ptr(value float64) *float64 {
	return &value
}

func calculateFlightSpeed(east, north, up float64) float64 {
	return math.Sqrt(east*east + north*north + up*up)
}

func cleanString(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "\x00")
	return strings.TrimSpace(value)
}
