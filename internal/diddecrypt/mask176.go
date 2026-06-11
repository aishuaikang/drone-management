package diddecrypt

import "strings"

const crc24Poly = 0x864CFB

var crc24Table [256]uint32

func init() {
	for i := 0; i < 256; i++ {
		crc := uint32(i) << 16
		for j := 0; j < 8; j++ {
			if crc&0x800000 != 0 {
				crc = (crc << 1) ^ crc24Poly
			} else {
				crc <<= 1
			}
		}
		crc24Table[i] = crc & 0xFFFFFF
	}
}

var (
	interleave176 = [176]int{
		101, 48, 167, 63, 1, 40, 27, 171, 74, 28, 117, 159, 21, 126, 138, 175,
		114, 125, 37, 149, 100, 110, 122, 4, 116, 42, 111, 174, 50, 57, 86, 107,
		83, 132, 95, 108, 47, 161, 148, 145, 141, 19, 98, 44, 87, 24, 137, 173,
		129, 55, 92, 163, 158, 153, 12, 93, 144, 103, 123, 155, 0, 30, 72, 109,
		79, 140, 61, 73, 99, 124, 118, 71, 146, 75, 166, 10, 39, 154, 14, 89,
		150, 18, 156, 172, 139, 151, 49, 59, 115, 7, 38, 58, 60, 128, 106, 162,
		68, 113, 17, 91, 15, 76, 2, 120, 168, 9, 84, 46, 131, 105, 85, 41,
		3, 134, 20, 77, 8, 104, 56, 90, 64, 94, 160, 152, 142, 52, 45, 164,
		165, 70, 97, 29, 67, 54, 51, 80, 121, 147, 35, 69, 31, 33, 22, 11,
		66, 96, 81, 130, 32, 25, 65, 127, 82, 119, 102, 170, 16, 88, 62, 136,
		6, 36, 5, 26, 34, 133, 43, 78, 112, 135, 143, 157, 169, 23, 53, 13,
	}

	mask176 = [176]byte{
		0xf2, 0x3b, 0x9b, 0x7c, 0xe3, 0xc2, 0x74, 0x05, 0xd1, 0x71, 0x9d, 0xca, 0xeb, 0xbc, 0x2d, 0x67,
		0xef, 0xea, 0x69, 0xe4, 0x0f, 0x5a, 0xcf, 0x03, 0x23, 0x34, 0x33, 0x9a, 0x45, 0x33, 0x04, 0xbe,
		0x71, 0xee, 0x77, 0x6b, 0xd8, 0x86, 0x34, 0xab, 0xd6, 0x05, 0xae, 0x61, 0xd4, 0x80, 0xb5, 0x6d,
		0x4e, 0x30, 0x31, 0xae, 0x4d, 0x8a, 0x26, 0xb2, 0x60, 0xdb, 0xda, 0x97, 0x7f, 0xe5, 0xd2, 0xa4,
		0xd1, 0xa8, 0x57, 0x4a, 0x57, 0x88, 0xb9, 0x4f, 0xd6, 0x91, 0x5e, 0xb3, 0x8b, 0x71, 0xb1, 0x9e,
		0xcb, 0xf4, 0x85, 0xe0, 0x2c, 0xfa, 0x45, 0x40, 0xdf, 0xbc, 0x23, 0x03, 0xe4, 0x33, 0x4c, 0xa9,
		0x49, 0x78, 0x11, 0xfc, 0x95, 0x6c, 0x83, 0x55, 0x6e, 0x3a, 0x94, 0xc2, 0x87, 0xa3, 0x35, 0x61,
		0xc8, 0xae, 0x76, 0x91, 0xcb, 0x0f, 0x9a, 0x0d, 0x6a, 0x4e, 0xdf, 0x04, 0xc4, 0xf8, 0xfc, 0xc9,
		0x70, 0x7f, 0x37, 0xa4, 0x52, 0xf5, 0xb9, 0x69, 0xbe, 0x44, 0x70, 0xee, 0xae, 0x36, 0xd6, 0xa0,
		0x22, 0x35, 0x9b, 0xa1, 0x5e, 0x93, 0x73, 0x0b, 0x07, 0x50, 0x03, 0x62, 0xae, 0x18, 0x09, 0x9c,
		0x9b, 0x04, 0x04, 0x30, 0x96, 0x0f, 0x5e, 0xa1, 0xb7, 0xb1, 0x15, 0x74, 0x71, 0x5a, 0x27, 0xac,
	}

	reverseInterleave176 = func() [176]int {
		var rev [176]int
		for i := 0; i < 176; i++ {
			rev[interleave176[i]] = i
		}
		return rev
	}()
)

var hexToNibble = [256]byte{
	'0': 0x0, '1': 0x1, '2': 0x2, '3': 0x3,
	'4': 0x4, '5': 0x5, '6': 0x6, '7': 0x7,
	'8': 0x8, '9': 0x9,
	'a': 0xa, 'b': 0xb, 'c': 0xc, 'd': 0xd, 'e': 0xe, 'f': 0xf,
	'A': 0xa, 'B': 0xb, 'C': 0xc, 'D': 0xd, 'E': 0xe, 'F': 0xf,
}

var nibbleToHex = [16]byte{'0', '1', '2', '3', '4', '5', '6', '7', '8', '9', 'a', 'b', 'c', 'd', 'e', 'f'}

func NormalizePacketHex(hexStr string) (encrypted string, decrypted string, ok bool) {
	hexStr = strings.ToLower(strings.TrimSpace(hexStr))
	if len(hexStr) != 352 || !isHexString(hexStr) {
		return "", "", false
	}
	if crc24Hex(hexStr) == 0 {
		return Normalize176Hex(hexStr, false), hexStr, true
	}
	decrypted = Normalize176Hex(hexStr, true)
	if crc24Hex(decrypted) == 0 {
		return hexStr, decrypted, true
	}
	return "", "", false
}

func Normalize176Hex(hexStr string, toDecrypted bool) string {
	if len(hexStr) != 352 {
		return hexStr
	}
	var result [352]byte
	if toDecrypted {
		var tmp [176]byte
		for i := 0; i < 176; i++ {
			hi := hexToNibble[hexStr[i*2]]
			lo := hexToNibble[hexStr[i*2+1]]
			tmp[i] = (hi<<4 | lo) ^ mask176[i]
		}
		for i := 0; i < 176; i++ {
			idx := interleave176[i]
			result[idx*2] = nibbleToHex[tmp[i]>>4]
			result[idx*2+1] = nibbleToHex[tmp[i]&0x0f]
		}
		return string(result[:])
	}
	var tmp [176]byte
	for i := 0; i < 176; i++ {
		hi := hexToNibble[hexStr[i*2]]
		lo := hexToNibble[hexStr[i*2+1]]
		tmp[reverseInterleave176[i]] = hi<<4 | lo
	}
	for i := 0; i < 176; i++ {
		b := tmp[i] ^ mask176[i]
		result[i*2] = nibbleToHex[b>>4]
		result[i*2+1] = nibbleToHex[b&0x0f]
	}
	return string(result[:])
}

func crc24Hex(hexStr string) uint32 {
	crc := uint32(0)
	for i := 0; i < len(hexStr); i += 2 {
		b := uint32(hexToNibble[hexStr[i]]<<4 | hexToNibble[hexStr[i+1]])
		crc = ((crc << 8) ^ crc24Table[((crc>>16)^b)&0xFF]) & 0xFFFFFF
	}
	return crc
}

func isHexString(value string) bool {
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			continue
		}
		return false
	}
	return true
}
