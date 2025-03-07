package featureprobe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type EventRecorder struct {
	auth           string
	eventsUrl      string
	flushInterval  time.Duration
	incomingEvents []AccessEvent
	packedData     []PackedData
	httpClient     http.Client
	mu             sync.Mutex
	wg             sync.WaitGroup
	startOnce      sync.Once
	stopOnce       sync.Once
	stopChan       chan struct{}
	ticker         *time.Ticker
}

type AccessEvent struct {
	Time    int64       `json:"time"`
	Key     string      `json:"key"`
	Value   interface{} `json:"value"`
	Index   *int        `json:"index"`
	Version *uint64     `json:"version"`
	Reason  string      `json:"reason"`
}

type PackedData struct {
	Events []AccessEvent `json:"events"`
	Access Access        `json:"access"`
}

type Access struct {
	StartTime int64                      `json:"startTime"`
	EndTime   int64                      `json:"endTime"`
	Counters  map[string][]ToggleCounter `json:"counters"`
}

type ToggleCounter struct {
	Value   interface{} `json:"value"`
	Version *uint64     `json:"version"`
	Index   *int        `json:"index"`
	Count   int         `json:"count"`
}

type Variation struct {
	Key     string  `json:"key"`
	Index   *int    `json:"index"`
	Version *uint64 `json:"version"`
}

type CountValue struct {
	Count int         `json:"count"`
	Value interface{} `json:"value"`
}

func NewEventRecorder(eventsUrl string, flushInterval time.Duration, auth string) EventRecorder {
	return EventRecorder{
		auth:           auth,
		eventsUrl:      eventsUrl,
		flushInterval:  flushInterval,
		incomingEvents: []AccessEvent{},
		packedData:     []PackedData{},
		httpClient:     newHttpClient(flushInterval),
		stopChan:       make(chan struct{}),
	}
}

func (e *EventRecorder) Start() {
	e.wg.Add(1)
	e.startOnce.Do(func() {
		e.ticker = time.NewTicker(e.flushInterval * time.Millisecond)
		go func() {
			for {
				select {
				case <-e.stopChan:
					e.doFlush()
					e.wg.Done()
					return
				case <-e.ticker.C:
					e.doFlush()
				}
			}
		}()
	})
}

func (e *EventRecorder) doFlush() {
	events := make([]AccessEvent, 0)
	e.mu.Lock()
	events, e.incomingEvents = e.incomingEvents, events
	e.mu.Unlock()
	if len(events) == 0 {
		return
	}
	packedData := e.buildPackedData(events)
	body, _ := json.Marshal(packedData)
	req, err := http.NewRequest(http.MethodPost, e.eventsUrl, bytes.NewBuffer(body))
	if err != nil {
		fmt.Printf("%s\n", err)
		return
	}
	req.Header.Add("Authorization", e.auth)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Add("User-Agent", USER_AGENT)
	_, err = e.httpClient.Do(req)
	if err != nil {
		fmt.Printf("Report event fails: %s\n", err)
	}
}

func (e *EventRecorder) buildPackedData(events []AccessEvent) []PackedData {
	access := e.buildAccess(events)
	p := PackedData{Access: access, Events: events}
	return []PackedData{p}
}

func (e *EventRecorder) buildAccess(events []AccessEvent) Access {
	counters, startTime, endTime := e.buildCounters(events)
	access := Access{
		StartTime: startTime,
		EndTime:   endTime,
		Counters:  map[string][]ToggleCounter{},
	}

	for k, v := range counters {
		counter := ToggleCounter{
			Index:   k.Index,
			Version: k.Version,
			Count:   v.Count,
			Value:   v.Value,
		}
		c, ok := access.Counters[k.Key]
		if !ok {
			access.Counters[k.Key] = []ToggleCounter{counter}
		} else {
			access.Counters[k.Key] = append(c, counter)
		}
	}
	return access
}

func (e *EventRecorder) buildCounters(events []AccessEvent) (map[Variation]CountValue, int64, int64) {
	var startTime *int64 = nil
	var endTime *int64 = nil
	counters := map[Variation]CountValue{}

	for _, event := range events {
		if startTime == nil || *startTime < event.Time {
			startTime = &event.Time
		}
		if endTime == nil || *endTime > event.Time {
			endTime = &event.Time
		}

		v := Variation{Key: event.Key, Version: event.Version, Index: event.Index}
		c, ok := counters[v]
		if !ok {
			counters[v] = CountValue{Count: 1, Value: event.Value}
		} else {
			c.Count += 1
		}
	}
	return counters, *startTime, *endTime
}

func (e *EventRecorder) RecordAccess(event AccessEvent) {
	e.mu.Lock()
	e.incomingEvents = append(e.incomingEvents, event)
	e.mu.Unlock()
}

func (e *EventRecorder) Stop() {
	if e.stopChan != nil {
		e.stopOnce.Do(func() {
			close(e.stopChan)
		})
	}
	e.wg.Wait()
}
