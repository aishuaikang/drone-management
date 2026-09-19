package protocol

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"drone-management/internal/model"
)

func TestDebugRecorderBoundsOrdersAndClearsRecords(t *testing.T) {
	recorder := NewDebugRecorder("test", 3)
	for index := 0; index < 5; index++ {
		recorder.Add(model.ProtocolDebugRecord{
			Topic: fmt.Sprintf("topic/%d", index),
			At:    time.Unix(int64(index), 0),
		})
	}
	records := recorder.List(2)
	if len(records) != 2 || records[0].Topic != "topic/4" || records[1].Topic != "topic/3" {
		t.Fatalf("records = %#v", records)
	}
	if records[0].Protocol != "test" || records[0].ID == "" {
		t.Fatalf("record metadata = %#v", records[0])
	}
	if cleared := recorder.Clear(); cleared != 3 {
		t.Fatalf("Clear() = %d, want 3", cleared)
	}
	if records := recorder.List(MaxDebugRecords); len(records) != 0 {
		t.Fatalf("records after Clear = %#v", records)
	}
}

func TestDebugRecorderListsByTimestampWhenRecordsArriveOutOfOrder(t *testing.T) {
	recorder := NewDebugRecorder("test", MaxDebugRecords)
	recorder.Add(model.ProtocolDebugRecord{Topic: "newest", At: time.Unix(30, 0)})
	recorder.Add(model.ProtocolDebugRecord{Topic: "oldest", At: time.Unix(10, 0)})
	recorder.Add(model.ProtocolDebugRecord{Topic: "middle", At: time.Unix(20, 0)})

	records := recorder.List(MaxDebugRecords)
	if len(records) != 3 || records[0].Topic != "newest" || records[1].Topic != "middle" || records[2].Topic != "oldest" {
		t.Fatalf("records = %#v", records)
	}
}

func TestDebugRecorderSupportsConcurrentReadersAndWriters(t *testing.T) {
	recorder := NewDebugRecorder("test", MaxDebugRecords)
	var wg sync.WaitGroup
	for index := 0; index < 200; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			recorder.Add(model.ProtocolDebugRecord{Topic: fmt.Sprintf("topic/%d", index)})
		}(index)
	}
	for index := 0; index < 25; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for read := 0; read < 20; read++ {
				_ = recorder.List(MaxDebugRecords)
			}
		}()
	}
	wg.Wait()
	records := recorder.List(MaxDebugRecords)
	if len(records) != MaxDebugRecords {
		t.Fatalf("record count = %d, want %d", len(records), MaxDebugRecords)
	}
	seen := map[string]struct{}{}
	for _, record := range records {
		if _, ok := seen[record.ID]; ok {
			t.Fatalf("duplicate record id %q", record.ID)
		}
		seen[record.ID] = struct{}{}
	}
}
