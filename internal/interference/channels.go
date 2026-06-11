package interference

import (
	"fmt"
	"sort"
)

var interferenceBands = [][]string{
	{"433", "800", "900", "1.4"},
	{"1.2", "1.5"},
	{"2.4", "5.2", "5.8"},
}

var interferencePinOrder = []int{2, 3, 1}
var reservedPinOrder = []int{4, 0, 5}

// ChannelDefinition declares one controllable external IO channel.
type ChannelDefinition struct {
	ID       string
	Label    string
	Pin      int
	Bands    []string
	Reserved bool
}

// DefaultChannels returns channel mappings for the current board.
func DefaultChannels() []ChannelDefinition {
	return ChannelsFromNumbers(ListExternalPins())
}

// ChannelsFromNumbers builds stable channel definitions from external IO numbers.
func ChannelsFromNumbers(numbers []int) []ChannelDefinition {
	if len(numbers) == 0 {
		return []ChannelDefinition{}
	}

	available := make(map[int]bool, len(numbers))
	for _, number := range numbers {
		if number >= 0 {
			available[number] = true
		}
	}

	definitions := make([]ChannelDefinition, 0, len(available))
	used := make(map[int]bool, len(available))
	for bandIndex, number := range interferencePinOrder {
		if !available[number] {
			continue
		}
		definitions = append(definitions, ChannelDefinition{
			ID:       fmt.Sprintf("io%d", bandIndex+1),
			Label:    fmt.Sprintf("IO%d", number),
			Pin:      number,
			Bands:    append([]string{}, interferenceBands[bandIndex]...),
			Reserved: false,
		})
		used[number] = true
	}

	for reservedIndex, number := range reservedPinOrder {
		if !available[number] || used[number] {
			continue
		}
		definitions = append(definitions, ChannelDefinition{
			ID:       fmt.Sprintf("io%d", len(interferencePinOrder)+reservedIndex+1),
			Label:    fmt.Sprintf("IO%d", number),
			Pin:      number,
			Bands:    []string{},
			Reserved: true,
		})
		used[number] = true
	}

	extraReserved := make([]int, 0, len(available)-len(used))
	for number := range available {
		if !used[number] {
			extraReserved = append(extraReserved, number)
		}
	}
	sort.Ints(extraReserved)
	nextExtraID := len(interferencePinOrder) + len(reservedPinOrder) + 1
	for _, number := range extraReserved {
		definitions = append(definitions, ChannelDefinition{
			ID:       fmt.Sprintf("io%d", nextExtraID),
			Label:    fmt.Sprintf("IO%d", number),
			Pin:      number,
			Bands:    []string{},
			Reserved: true,
		})
		nextExtraID++
	}
	return definitions
}
