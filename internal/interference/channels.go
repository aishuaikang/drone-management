package interference

import "fmt"

var interferenceBands = [][]string{
	{"433"},
	{"915"},
	{"1.2"},
	{"1.4"},
	{"1.5"},
	{"2.4"},
	{"5.2"},
	{"5.8"},
}

// ChannelDefinition declares one relay output channel.
type ChannelDefinition struct {
	ID       string
	Label    string
	Output   int
	Bands    []string
	Reserved bool
}

// DefaultChannels returns channel mappings for the 8-channel network relay.
func DefaultChannels() []ChannelDefinition {
	definitions := make([]ChannelDefinition, 0, relayChannelCount)
	for number := 1; number <= relayChannelCount; number++ {
		index := number - 1
		def := ChannelDefinition{
			ID:       fmt.Sprintf("io%d", number),
			Label:    fmt.Sprintf("Y%d", number),
			Output:   number,
			Bands:    []string{},
			Reserved: false,
		}
		if index < len(interferenceBands) {
			def.Bands = append([]string{}, interferenceBands[index]...)
		}
		definitions = append(definitions, def)
	}
	return definitions
}
