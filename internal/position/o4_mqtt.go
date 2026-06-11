package position

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"dr600ab-net/internal/diddecrypt"
	"dr600ab-net/internal/model"
)

const o4MQTTDefaultRequestQoS = byte(1)

// O3DecryptOptions configures O3+/O4 DID MQTT decryption.
type O3DecryptOptions struct {
	Enabled        bool
	Broker         string
	Port           int
	Username       string
	Password       string
	Timeout        time.Duration
	ConnectTimeout time.Duration
}

type mqttO4DIDDecoder struct {
	options O3DecryptOptions

	decoderMu sync.Mutex
	decoder   *diddecrypt.Decoder

	mu            sync.Mutex
	client        mqtt.Client
	subMu         sync.Mutex
	subscriptions sync.Map
	responses     sync.Map
	statuses      sync.Map
	seenPackets   sync.Map
}

type o4DecryptRequest struct {
	RequestID string `json:"request_id"`
	Data      string `json:"data"`
}

type o4DecryptResponse struct {
	RequestID string                   `json:"request_id"`
	Success   bool                     `json:"success"`
	Message   string                   `json:"message"`
	Data      diddecrypt.DecryptResult `json:"data"`
}

// NewMQTTO4DIDDecoder creates a DID decryptor. Incomplete config disables it.
func NewMQTTO4DIDDecoder(options O3DecryptOptions) DIDDecoder {
	options.Broker = strings.TrimSpace(options.Broker)
	options.Username = strings.TrimSpace(options.Username)
	if !options.Enabled || options.Broker == "" || options.Port <= 0 || options.Username == "" {
		return nil
	}
	if options.Timeout <= 0 {
		options.Timeout = 10 * time.Second
	}
	if options.ConnectTimeout <= 0 {
		options.ConnectTimeout = 10 * time.Second
	}

	decoder := &mqttO4DIDDecoder{options: options}
	decoder.decoder = diddecrypt.NewDecoder(decoder, diddecrypt.Options{
		RequireDecodedCoordinate: true,
	})
	return decoder
}

func (d *mqttO4DIDDecoder) DecodeDID(
	ctx context.Context,
	packet diddecrypt.Packet,
	raw string,
	receivedAt time.Time,
) (model.ScreenPositionTarget, bool) {
	if d == nil {
		return model.ScreenPositionTarget{}, false
	}

	deviceSN := deviceSNForDIDPacket(packet)
	d.logDecodeStart(packet, deviceSN)
	out := d.didDecoder().Decode(ctx, packet, deviceSN, receivedAt)
	d.logDecodeOutput(packet, deviceSN, out)
	if out.Err != nil || !out.HasTarget {
		return model.ScreenPositionTarget{}, false
	}
	target := out.Target
	if !target.Cracked {
		return model.ScreenPositionTarget{}, false
	}
	if target.LastRecord.Raw == "" {
		target.LastRecord.Raw = raw
	}
	data := map[string]string{
		"encryptedID": packet.EncryptedID,
		"status":      string(out.Status),
	}
	dataJSON, _ := json.Marshal(data)
	target.LastRecord.Data = dataJSON
	return target, target.Drone != nil || target.Pilot != nil || target.Home != nil
}

func (d *mqttO4DIDDecoder) logDecodeStart(packet diddecrypt.Packet, deviceSN string) {
	diagnostics := diagnoseDIDPacket(packet)
	dedupeKey := strings.Join([]string{
		strings.ToLower(strings.TrimSpace(packet.EncryptedID)),
		diagnostics.RawType,
		diagnostics.NormalizedType,
		"received",
	}, "|")
	if _, loaded := d.seenPackets.LoadOrStore(dedupeKey, struct{}{}); loaded {
		return
	}
	slog.Info(
		"DID 包进入联网解密",
		"encrypted_id", packet.EncryptedID,
		"device_sn", deviceSN,
		"raw_type", diagnostics.RawType,
		"raw_magic", diagnostics.RawMagic,
		"normalized_ok", diagnostics.NormalizedOK,
		"normalized_type", diagnostics.NormalizedType,
		"normalized_kind", diagnostics.NormalizedKind,
		"bytes", diagnostics.ByteLen,
	)
}

func (d *mqttO4DIDDecoder) logDecodeOutput(
	packet diddecrypt.Packet,
	deviceSN string,
	out diddecrypt.Output,
) {
	diagnostics := diagnoseDIDPacket(packet)
	dedupeKey := strings.Join([]string{
		strings.ToLower(strings.TrimSpace(packet.EncryptedID)),
		diagnostics.RawType,
		diagnostics.NormalizedType,
	}, "|")
	state := string(out.Status)
	if out.HasTarget {
		state += "|target"
	}
	if out.Err != nil {
		state += "|" + out.Err.Error()
	}
	if previous, ok := d.statuses.Load(dedupeKey); ok && previous == state {
		return
	}
	d.statuses.Store(dedupeKey, state)

	attrs := []any{
		"encrypted_id", packet.EncryptedID,
		"device_sn", deviceSN,
		"raw_type", diagnostics.RawType,
		"raw_magic", diagnostics.RawMagic,
		"normalized_ok", diagnostics.NormalizedOK,
		"normalized_type", diagnostics.NormalizedType,
		"normalized_kind", diagnostics.NormalizedKind,
		"bytes", diagnostics.ByteLen,
		"status", out.Status,
		"has_target", out.HasTarget,
	}
	if out.Err != nil {
		attrs = append(attrs, "error", out.Err)
	}
	if out.HasTarget {
		attrs = append(
			attrs,
			"serial", out.Target.Serial,
			"model", out.Target.Model,
			"has_drone", out.Target.Drone != nil,
			"has_pilot", out.Target.Pilot != nil,
			"has_home", out.Target.Home != nil,
		)
	}

	switch {
	case out.Err != nil:
		slog.Warn("DID 联网解密失败", attrs...)
	case out.HasTarget:
		slog.Info("DID 联网解密得到目标", attrs...)
	case out.Status == diddecrypt.StatusKeyCached ||
		out.Status == diddecrypt.StatusKeyAlreadyCached:
		slog.Info("DID 密钥包已处理", attrs...)
	case out.Status == diddecrypt.StatusPendingKey:
		slog.Info("DID 动态包等待密钥", attrs...)
	case out.Status == diddecrypt.StatusUnsupported:
		slog.Info("DID 包型暂不支持", attrs...)
	default:
		slog.Info("DID 联网解密未生成目标", attrs...)
	}
}

type didPacketDiagnostics struct {
	RawType        string
	RawMagic       string
	NormalizedOK   bool
	NormalizedType string
	NormalizedKind string
	ByteLen        int
}

func diagnoseDIDPacket(packet diddecrypt.Packet) didPacketDiagnostics {
	hexStr := strings.ToLower(strings.TrimSpace(packet.Bytes))
	diagnostics := didPacketDiagnostics{ByteLen: len(hexStr) / 2}
	if len(hexStr) >= 2 {
		diagnostics.RawType = hexStr[:2]
	}
	if len(hexStr) >= 12 {
		diagnostics.RawMagic = didMagicLabel(hexStr[4:12])
	}

	_, decryptedHex, ok := diddecrypt.NormalizePacketHex(hexStr)
	diagnostics.NormalizedOK = ok
	if ok && len(decryptedHex) >= 2 {
		diagnostics.NormalizedType = decryptedHex[:2]
		diagnostics.NormalizedKind = packetTypeLabel(diddecrypt.PacketTypeFromHex(decryptedHex))
	}
	return diagnostics
}

func didMagicLabel(magicHex string) string {
	switch strings.ToLower(magicHex) {
	case "494e4650":
		return "INFP"
	case "43525950":
		return "CRYP"
	default:
		return strings.ToLower(magicHex)
	}
}

func packetTypeLabel(packetType diddecrypt.PacketType) string {
	switch packetType {
	case diddecrypt.PacketDirect:
		return "direct"
	case diddecrypt.PacketKey:
		return "key"
	case diddecrypt.PacketDynamic:
		return "dynamic"
	default:
		return "unknown"
	}
}

func (d *mqttO4DIDDecoder) didDecoder() *diddecrypt.Decoder {
	d.decoderMu.Lock()
	defer d.decoderMu.Unlock()
	if d.decoder == nil {
		d.decoder = diddecrypt.NewDecoder(d, diddecrypt.Options{
			RequireDecodedCoordinate: true,
		})
	}
	return d.decoder
}

func (d *mqttO4DIDDecoder) Decrypt(
	ctx context.Context,
	req diddecrypt.Request,
) (diddecrypt.DecryptResult, error) {
	resp, err := d.publishAndWait(ctx, req.DecryptedHex, req.DeviceSN)
	if err != nil {
		return diddecrypt.DecryptResult{}, err
	}
	if resp == nil {
		return diddecrypt.DecryptResult{}, fmt.Errorf("empty MQTT decrypt response")
	}
	if !resp.Success {
		return diddecrypt.DecryptResult{}, fmt.Errorf("MQTT decrypt failed: %s", resp.Message)
	}
	return resp.Data, nil
}

func (d *mqttO4DIDDecoder) SendKeyPacket(
	ctx context.Context,
	req diddecrypt.Request,
) diddecrypt.KeyResult {
	resp, err := d.publishAndWait(ctx, req.DecryptedHex, req.DeviceSN)
	if err != nil {
		return diddecrypt.KeyResult{Err: err}
	}
	if resp == nil {
		return diddecrypt.KeyResult{Err: fmt.Errorf("empty MQTT keygen response")}
	}
	msg := strings.TrimSpace(resp.Data.Msg)
	return diddecrypt.KeyResult{
		Msg:     msg,
		Note:    resp.Data.Note,
		Success: msg == "keygen_succ" || msg == "key_exist",
		Data:    resp.Data,
	}
}

func (d *mqttO4DIDDecoder) publishAndWait(
	ctx context.Context,
	hexStr string,
	deviceSN string,
) (*o4DecryptResponse, error) {
	if err := d.ensureClient(); err != nil {
		return nil, err
	}
	if err := d.ensureSubscription(deviceSN); err != nil {
		return nil, err
	}

	timeout := d.options.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	requestID := randomHexID()
	responseCh := make(chan *o4DecryptResponse, 1)
	d.responses.Store(requestID, responseCh)
	defer d.responses.Delete(requestID)

	requestBytes, err := json.Marshal(o4DecryptRequest{
		RequestID: requestID,
		Data:      hexStr,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal MQTT decrypt request: %w", err)
	}

	requestTopic := fmt.Sprintf("%s/%s", d.options.Username, deviceSN)
	token := d.client.Publish(requestTopic, o4MQTTDefaultRequestQoS, false, requestBytes)
	if !token.WaitTimeout(2 * time.Second) {
		return nil, fmt.Errorf("publish MQTT decrypt request timeout")
	}
	if err := token.Error(); err != nil {
		return nil, fmt.Errorf("publish MQTT decrypt request: %w", err)
	}

	select {
	case resp := <-responseCh:
		return resp, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("MQTT decrypt timeout: %w", ctx.Err())
	}
}

func (d *mqttO4DIDDecoder) ensureClient() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.client != nil && d.client.IsConnected() {
		return nil
	}

	opts := mqtt.NewClientOptions().
		AddBroker(fmt.Sprintf("tcp://%s:%d", d.options.Broker, d.options.Port)).
		SetClientID("dr600ab_net_" + randomHexID()[:8]).
		SetUsername(d.options.Username).
		SetPassword(d.options.Password).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second).
		SetCleanSession(false).
		SetResumeSubs(true).
		SetConnectTimeout(d.options.ConnectTimeout)

	client := mqtt.NewClient(opts)
	token := client.Connect()
	if !token.WaitTimeout(d.options.ConnectTimeout) {
		return fmt.Errorf("connect MQTT decrypt server timeout")
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("connect MQTT decrypt server: %w", err)
	}

	d.client = client
	d.subscriptions.Range(func(key, _ any) bool {
		d.subscriptions.Delete(key)
		return true
	})
	return nil
}

func (d *mqttO4DIDDecoder) ensureSubscription(deviceSN string) error {
	if _, ok := d.subscriptions.Load(deviceSN); ok {
		return nil
	}

	d.subMu.Lock()
	defer d.subMu.Unlock()
	if _, ok := d.subscriptions.Load(deviceSN); ok {
		return nil
	}
	if err := d.ensureClient(); err != nil {
		return err
	}

	responseTopic := fmt.Sprintf("%s/%s/response", d.options.Username, deviceSN)
	token := d.client.Subscribe(responseTopic, o4MQTTDefaultRequestQoS, d.handleMessage)
	if !token.WaitTimeout(3 * time.Second) {
		return fmt.Errorf("subscribe MQTT decrypt response timeout")
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("subscribe MQTT decrypt response: %w", err)
	}

	d.subscriptions.Store(deviceSN, struct{}{})
	return nil
}

func (d *mqttO4DIDDecoder) handleMessage(_ mqtt.Client, message mqtt.Message) {
	var resp o4DecryptResponse
	if err := json.Unmarshal(message.Payload(), &resp); err != nil || resp.RequestID == "" {
		return
	}
	value, ok := d.responses.Load(resp.RequestID)
	if !ok {
		return
	}
	ch, ok := value.(chan *o4DecryptResponse)
	if !ok {
		return
	}
	select {
	case ch <- &resp:
	default:
	}
}

func deviceSNForDIDPacket(packet diddecrypt.Packet) string {
	if device := strings.TrimSpace(packet.Device); device != "" {
		return device
	}
	if encryptedID := strings.TrimSpace(packet.EncryptedID); encryptedID != "" {
		return encryptedID
	}
	return "dr600ab-net"
}

func randomHexID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err == nil {
		return hex.EncodeToString(bytes[:])
	}
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
