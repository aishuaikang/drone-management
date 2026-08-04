package coordinate

import (
	"math"
	"testing"
)

func TestIsValid(t *testing.T) {
	tests := []struct {
		name      string
		longitude float64
		latitude  float64
		want      bool
	}{
		{name: "valid", longitude: 117.00837, latitude: 28.19329, want: true},
		{name: "boundary threshold", longitude: 0.1, latitude: -0.1, want: true},
		{name: "zero longitude", longitude: 0, latitude: 28.19329, want: false},
		{name: "zero latitude", longitude: 117.00837, latitude: 0, want: false},
		{name: "near origin", longitude: 0.05, latitude: -0.05, want: false},
		{name: "longitude outside range", longitude: 181, latitude: 28.19329, want: false},
		{name: "latitude outside range", longitude: 117.00837, latitude: 91, want: false},
		{name: "longitude NaN", longitude: math.NaN(), latitude: 28.19329, want: false},
		{name: "latitude infinity", longitude: 117.00837, latitude: math.Inf(1), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValid(tt.longitude, tt.latitude); got != tt.want {
				t.Fatalf("IsValid(%v, %v) = %t, want %t", tt.longitude, tt.latitude, got, tt.want)
			}
		})
	}
}

func TestIsValidCoordRejectsNil(t *testing.T) {
	longitude := 117.00837
	latitude := 28.19329
	if IsValidCoord(nil, &latitude) {
		t.Fatal("IsValidCoord(nil, latitude) = true, want false")
	}
	if IsValidCoord(&longitude, nil) {
		t.Fatal("IsValidCoord(longitude, nil) = true, want false")
	}
	if !IsValidCoord(&longitude, &latitude) {
		t.Fatal("IsValidCoord(valid longitude, valid latitude) = false, want true")
	}
}
