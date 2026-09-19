package protocol

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"drone-management/internal/model"
)

const MaxDebugRecords = 100

const (
	DebugDirectionOutbound = "outbound"
	DebugDirectionInbound  = "inbound"

	DebugSourceAutomatic = "automatic"
	DebugSourceManual    = "manual"
	DebugSourceBroker    = "broker"

	DebugEncodingPlainJSON    = "plain-json"
	DebugEncodingSM4CBCBase64 = "sm4-cbc-base64"

	DebugOutcomeSuccess = "success"
	DebugOutcomeError   = "error"
	DebugOutcomeIgnored = "ignored"
)

// DebugRecorder keeps a bounded, process-local diagnostic history in newest-first order.
type DebugRecorder struct {
	mu       sync.RWMutex
	protocol string
	capacity int
	sequence uint64
	records  []model.ProtocolDebugRecord
	next     int
	count    int
}

func NewDebugRecorder(protocolName string, capacity int) *DebugRecorder {
	if capacity <= 0 || capacity > MaxDebugRecords {
		capacity = MaxDebugRecords
	}
	return &DebugRecorder{
		protocol: protocolName,
		capacity: capacity,
		records:  make([]model.ProtocolDebugRecord, capacity),
	}
}

func (r *DebugRecorder) Add(record model.ProtocolDebugRecord) model.ProtocolDebugRecord {
	if r == nil {
		return record
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if record.At.IsZero() {
		record.At = time.Now()
	}
	if record.Protocol == "" {
		record.Protocol = r.protocol
	}
	r.sequence++
	if record.ID == "" {
		record.ID = fmt.Sprintf("%s-%d-%d", record.Protocol, record.At.UnixNano(), r.sequence)
	}
	r.records[r.next] = record
	r.next = (r.next + 1) % r.capacity
	if r.count < r.capacity {
		r.count++
	}
	return record
}

func (r *DebugRecorder) List(limit int) []model.ProtocolDebugRecord {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if limit <= 0 || limit > r.count {
		limit = r.count
	}
	records := make([]model.ProtocolDebugRecord, 0, r.count)
	for offset := 0; offset < r.count; offset++ {
		index := (r.next - 1 - offset + r.capacity) % r.capacity
		records = append(records, r.records[index])
	}
	sort.SliceStable(records, func(left, right int) bool {
		return records[left].At.After(records[right].At)
	})
	return records[:limit]
}

func (r *DebugRecorder) Clear() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	count := r.count
	r.records = make([]model.ProtocolDebugRecord, r.capacity)
	r.next = 0
	r.count = 0
	return count
}
