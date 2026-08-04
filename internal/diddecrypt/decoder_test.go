package diddecrypt

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeClient struct {
	decryptErr error
	keyResult  KeyResult
}

type zeroCoordinateClient struct{}

func (f fakeClient) Decrypt(context.Context, Request) (DecryptResult, error) {
	if f.decryptErr != nil {
		return DecryptResult{}, f.decryptErr
	}
	return DecryptResult{
		SN:       "real-sn",
		Model:    "DJI Mini 4 Pro",
		Lon:      121.4,
		Lat:      31.2,
		Height:   35,
		PilotLon: 121.41,
		PilotLat: 31.21,
	}, nil
}

func (f fakeClient) SendKeyPacket(context.Context, Request) KeyResult {
	return f.keyResult
}

func (zeroCoordinateClient) Decrypt(context.Context, Request) (DecryptResult, error) {
	return DecryptResult{
		SN:    "zero-sn",
		Model: "DJI Mini 4 Pro",
		Lon:   0,
		Lat:   0,
	}, nil
}

func (zeroCoordinateClient) SendKeyPacket(context.Context, Request) KeyResult {
	return KeyResult{}
}

func TestPacketTypeFromHex(t *testing.T) {
	tests := []struct {
		name string
		hex  string
		want PacketType
	}{
		{name: "direct", hex: "6d00", want: PacketDirect},
		{name: "key aa", hex: "aa00", want: PacketKey},
		{name: "key a3", hex: "a300", want: PacketKey},
		{name: "dynamic", hex: "8700", want: PacketDynamic},
		{name: "unknown", hex: "ff00", want: PacketUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PacketTypeFromHex(tt.hex); got != tt.want {
				t.Fatalf("PacketTypeFromHex() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNormalizePacketHexPreserves180ByteLength(t *testing.T) {
	core := "8710494e4650447e5681" + strings.Repeat("00", 163) + "a163b7"
	tail := "aabbccdd"
	encrypted, decrypted, ok := NormalizePacketHex(core + tail)
	if !ok {
		t.Fatal("NormalizePacketHex() rejected 180-byte packet")
	}
	if len(encrypted) != 360 || len(decrypted) != 360 {
		t.Fatalf("normalized lengths = %d/%d, want 360/360", len(encrypted), len(decrypted))
	}
	if !strings.HasSuffix(encrypted, tail) || !strings.HasSuffix(decrypted, tail) {
		t.Fatalf("180-byte tail was changed: encrypted=%q decrypted=%q", encrypted[len(encrypted)-8:], decrypted[len(decrypted)-8:])
	}
}

func TestDecoderDecodeHexPairDirectSuccess(t *testing.T) {
	decoder := NewDecoder(fakeClient{}, Options{})
	out := decoder.decodeHexPair(context.Background(), packet(), "6d00", "6d00", "device-sn", time.Unix(1700000000, 0))
	if out.Status != StatusDecoded || !out.HasTarget {
		t.Fatalf("output = %+v", out)
	}
	if out.Target.Serial != "real-sn" || out.Target.Model != "DJI Mini 4 Pro" || !out.Target.Cracked {
		t.Fatalf("target = %+v", out.Target)
	}
	if out.Target.Drone == nil || out.Target.Drone.Latitude != 31.2 || out.Target.Drone.Longitude != 121.4 {
		t.Fatalf("drone point = %+v", out.Target.Drone)
	}
}

func TestDecoderRequireDecodedCoordinateRejectsZeroPoint(t *testing.T) {
	decoder := NewDecoder(zeroCoordinateClient{}, Options{RequireDecodedCoordinate: true})
	out := decoder.decodeHexPair(context.Background(), packet(), "6d00", "6d00", "device-sn", time.Unix(1700000000, 0))
	if out.Status != StatusDecoded || out.HasTarget {
		t.Fatalf("output = %+v, want decoded result without target", out)
	}
	if out.Target.Drone != nil || out.Target.Pilot != nil || out.Target.Home != nil {
		t.Fatalf("invalid coordinates produced target points: %+v", out.Target)
	}
}

func TestDecoderDecodeHexPairDynamicPendingKeyCanEmitUncrackedTarget(t *testing.T) {
	decoder := NewDecoder(fakeClient{}, Options{EmitUncrackedTarget: true})
	out := decoder.decodeHexPair(context.Background(), packet(), "8700", "8700", "device-sn", time.Unix(1700000000, 0))
	if out.Status != StatusPendingKey || !out.HasTarget {
		t.Fatalf("output = %+v", out)
	}
	if out.Target.Model != FallbackModel || out.Target.Serial != "447e5681" || out.Target.Cracked {
		t.Fatalf("target = %+v", out.Target)
	}
}

func TestDecoderDecodeHexPairDirectFailureCanSuppressUncrackedTarget(t *testing.T) {
	decoder := NewDecoder(fakeClient{decryptErr: errors.New("timeout")}, Options{})
	out := decoder.decodeHexPair(context.Background(), packet(), "6d00", "6d00", "device-sn", time.Unix(1700000000, 0))
	if out.Status != StatusUncracked || out.HasTarget {
		t.Fatalf("output = %+v", out)
	}
}

func TestDecoderCachesKeyPacket(t *testing.T) {
	decoder := NewDecoder(fakeClient{keyResult: KeyResult{Success: true, Msg: "keygen_succ"}}, Options{})
	out := decoder.decodeHexPair(context.Background(), packet(), "aa00", "aa00", "device-sn", time.Unix(1700000000, 0))
	if out.Status != StatusKeyCached {
		t.Fatalf("output = %+v", out)
	}
	out = decoder.decodeHexPair(context.Background(), packet(), "8700", "8700", "device-sn", time.Unix(1700000001, 0))
	if out.Status != StatusDecoded || !out.HasTarget {
		t.Fatalf("output after key = %+v", out)
	}
}

func packet() Packet {
	return Packet{
		Device:      "device-sn",
		EncryptedID: "447e5681",
		Freq:        5776.5,
		RSSI:        -83,
	}
}
