package onvif

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/ohcnetwork/mock-ptz-camera/ptz"
)

const maxEventsPerSub = 100

type Subscription struct {
	ID              string
	TerminationTime time.Time
	events          []NotificationMessage
	notify          chan struct{}
	mu              sync.Mutex
}

type EventsService struct {
	subs   map[string]*Subscription
	mu     sync.RWMutex
	subURL string
}

func NewEventsService(subBaseURL string) *EventsService {
	es := &EventsService{
		subs:   make(map[string]*Subscription),
		subURL: subBaseURL,
	}
	go es.cleanupLoop()
	return es
}

func (es *EventsService) OnPTZPositionChanged(status ptz.Status) {
	msg := NotificationMessage{
		Topic: TopicExpression{
			Dialect: "http://www.onvif.org/ver10/tev/topicExpression/ConcreteSet",
			Value:   "tns1:PTZ/Position/Changed",
		},
		Message: MessageContent{
			UtcTime: time.Now().UTC().Format(time.RFC3339),
			Source: &SimpleItem{
				Name:  "ProfileToken",
				Value: "MainProfile",
			},
			Data: &SimpleItem{
				Name:  "Position",
				Value: fmt.Sprintf("Pan=%.4f Tilt=%.4f Zoom=%.4f", status.Position.Pan, status.Position.Tilt, status.Position.Zoom),
			},
		},
	}

	es.mu.RLock()
	defer es.mu.RUnlock()
	for _, sub := range es.subs {
		sub.mu.Lock()
		if len(sub.events) >= maxEventsPerSub {
			sub.events = sub.events[1:]
		}
		sub.events = append(sub.events, msg)
		sub.mu.Unlock()
		select {
		case sub.notify <- struct{}{}:
		default:
		}
	}
}

func (es *EventsService) createSubscription(timeout time.Duration) *Subscription {
	id := fmt.Sprintf("sub-%d-%d", time.Now().UnixNano(), rand.Intn(10000))
	sub := &Subscription{
		ID:              id,
		TerminationTime: time.Now().Add(timeout),
		notify:          make(chan struct{}, 1),
	}
	es.mu.Lock()
	es.subs[id] = sub
	es.mu.Unlock()
	return sub
}

func (es *EventsService) getSub(id string) *Subscription {
	es.mu.RLock()
	defer es.mu.RUnlock()
	return es.subs[id]
}

func (es *EventsService) removeSub(id string) {
	es.mu.Lock()
	delete(es.subs, id)
	es.mu.Unlock()
}

func (es *EventsService) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		es.mu.Lock()
		for id, sub := range es.subs {
			if now.After(sub.TerminationTime) {
				delete(es.subs, id)
				log.WithField("subscription", id).Debug("cleaned up expired subscription")
			}
		}
		es.mu.Unlock()
	}
}

func (s *Server) getEventProperties(w http.ResponseWriter) {
	writeSOAPResponse(w, renderTemplate("getEventProperties", nil))
}

func (s *Server) createPullPointSubscription(w http.ResponseWriter, env *SOAPEnvelope) {
	timeout := 60 * time.Second
	var req struct {
		XMLName                xml.Name `xml:"CreatePullPointSubscription"`
		InitialTerminationTime string   `xml:"InitialTerminationTime"`
	}
	if err := xml.NewDecoder(bytes.NewReader(env.Body.Content)).Decode(&req); err == nil && req.InitialTerminationTime != "" {
		if d, err := parsePTDuration(req.InitialTerminationTime); err == nil {
			timeout = d
		}
	}

	sub := s.events.createSubscription(timeout)
	refURL := fmt.Sprintf("%s?id=%s", s.serviceURL("/onvif/subscription"), sub.ID)
	body := renderTemplate("createPullPointSubscription", subscriptionData{
		Address:         refURL,
		CurrentTime:     time.Now().UTC().Format(time.RFC3339),
		TerminationTime: sub.TerminationTime.UTC().Format(time.RFC3339),
	})
	writeSOAPResponse(w, body)
}

func (s *Server) pullMessages(w http.ResponseWriter, env *SOAPEnvelope, subID string) {
	sub := s.events.getSub(subID)
	if sub == nil {
		writeSOAPFault(w, "s:Receiver", "Subscription not found")
		return
	}

	var req struct {
		XMLName      xml.Name `xml:"PullMessages"`
		Timeout      string   `xml:"Timeout"`
		MessageLimit int      `xml:"MessageLimit"`
	}
	timeout := 10 * time.Second
	limit := 10
	if err := xml.NewDecoder(bytes.NewReader(env.Body.Content)).Decode(&req); err == nil {
		if req.MessageLimit > 0 {
			limit = req.MessageLimit
		}
		if d, err := parsePTDuration(req.Timeout); err == nil {
			timeout = d
		}
	}

	sub.mu.Lock()
	events := sub.events
	sub.events = nil
	sub.mu.Unlock()

	if len(events) == 0 {
		timer := time.NewTimer(timeout)
		select {
		case <-sub.notify:
			timer.Stop()
			sub.mu.Lock()
			events = sub.events
			sub.events = nil
			sub.mu.Unlock()
		case <-timer.C:
		}
	}

	if len(events) > limit {
		events = events[:limit]
	}

	var msgsXML string
	for _, ev := range events {
		msgsXML += renderTemplate("notificationMessage", notificationData{
			Dialect:     ev.Topic.Dialect,
			Topic:       ev.Topic.Value,
			UtcTime:     ev.Message.UtcTime,
			SourceName:  ev.Message.Source.Name,
			SourceValue: ev.Message.Source.Value,
			DataName:    ev.Message.Data.Name,
			DataValue:   ev.Message.Data.Value,
		})
	}

	body := renderTemplate("pullMessagesResponse", pullMessagesData{
		CurrentTime:     time.Now().UTC().Format(time.RFC3339),
		TerminationTime: sub.TerminationTime.UTC().Format(time.RFC3339),
		Messages:        msgsXML,
	})
	writeSOAPResponse(w, body)
}

func (s *Server) renewSubscription(w http.ResponseWriter, subID string) {
	sub := s.events.getSub(subID)
	if sub == nil {
		writeSOAPFault(w, "s:Receiver", "Subscription not found")
		return
	}
	sub.mu.Lock()
	sub.TerminationTime = time.Now().Add(60 * time.Second)
	termTime := sub.TerminationTime
	sub.mu.Unlock()

	body := renderTemplate("renewResponse", renewData{
		TerminationTime: termTime.UTC().Format(time.RFC3339),
		CurrentTime:     time.Now().UTC().Format(time.RFC3339),
	})
	writeSOAPResponse(w, body)
}

func (s *Server) unsubscribe(w http.ResponseWriter, subID string) {
	s.events.removeSub(subID)
	writeSOAPResponse(w, `<wsnt:UnsubscribeResponse/>`)
}

func parsePTDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "PT") {
		return 0, fmt.Errorf("invalid duration: %s", s)
	}
	s = s[2:]
	var total time.Duration
	for len(s) > 0 {
		i := 0
		for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
			i++
		}
		if i == 0 || i >= len(s) {
			return 0, fmt.Errorf("invalid duration segment: %s", s)
		}
		val, err := strconv.ParseFloat(s[:i], 64)
		if err != nil {
			return 0, err
		}
		unit := s[i]
		s = s[i+1:]
		switch unit {
		case 'H':
			total += time.Duration(val * float64(time.Hour))
		case 'M':
			total += time.Duration(val * float64(time.Minute))
		case 'S':
			total += time.Duration(val * float64(time.Second))
		default:
			return 0, fmt.Errorf("unknown duration unit: %c", unit)
		}
	}
	return total, nil
}
