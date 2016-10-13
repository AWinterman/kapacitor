package victorops

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/influxdata/kapacitor"
)

type Service struct {
	mu         sync.RWMutex
	enabled    bool
	routingKey string
	url        string
	global     bool
	logger     *log.Logger
}

func NewService(c Config, l *log.Logger) *Service {
	return &Service{
		enabled:    c.Enabled,
		routingKey: c.RoutingKey,
		url:        c.URL + "/" + c.APIKey + "/",
		global:     c.Global,
		logger:     l,
	}
}

func (s *Service) Open() error {
	return nil
}

func (s *Service) Close() error {
	return nil
}

func (s *Service) Update(newConfig []interface{}) error {
	if l := len(newConfig); l != 1 {
		return fmt.Errorf("expected only one new config object, got %d", l)
	}
	if c, ok := newConfig[0].(Config); !ok {
		return fmt.Errorf("expected config object to be of type %T, got %T", c, newConfig[0])
	} else {
		s.mu.Lock()
		s.enabled = c.Enabled
		s.routingKey = c.RoutingKey
		s.url = c.URL + "/" + c.APIKey + "/"
		s.global = c.Global
		s.mu.Unlock()
	}
	return nil
}

func (s *Service) Global() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.global
}

type testOptions struct {
	RoutingKey  string `json:"routingKey"`
	MessageType string `json:"messageType"`
	Message     string `json:"message"`
	EntityID    string `json:"entityID"`
}

func (s *Service) TestOptions() interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return &testOptions{
		RoutingKey:  s.routingKey,
		MessageType: "CRITICAL",
		Message:     "test victorops message",
		EntityID:    "testEntityID",
	}
}

func (s *Service) Test(options interface{}) error {
	o, ok := options.(*testOptions)
	if !ok {
		return fmt.Errorf("unexpected options type %T", options)
	}
	return s.Alert(
		o.RoutingKey,
		o.MessageType,
		o.Message,
		o.EntityID,
		time.Now(),
		nil,
	)
}

func (s *Service) Alert(routingKey, messageType, message, entityID string, t time.Time, details interface{}) error {
	url, post, err := s.preparePost(routingKey, messageType, message, entityID, t, details)
	if err != nil {
		return err
	}

	resp, err := http.Post(url, "application/json", post)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return errors.New("URL or API key not found: 404")
		}
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		type response struct {
			Message string `json:"message"`
		}
		r := &response{Message: fmt.Sprintf("failed to understand VictorOps response. code: %d content: %s", resp.StatusCode, string(body))}
		b := bytes.NewReader(body)
		dec := json.NewDecoder(b)
		dec.Decode(r)
		return errors.New(r.Message)
	}
	return nil
}

func (s *Service) preparePost(routingKey, messageType, message, entityID string, t time.Time, details interface{}) (string, io.Reader, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.enabled {
		return "", nil, errors.New("service is not enabled")
	}

	voData := make(map[string]interface{})
	voData["message_type"] = messageType
	voData["entity_id"] = entityID
	voData["state_message"] = message
	voData["timestamp"] = t.Unix()
	voData["monitoring_tool"] = kapacitor.Product
	if details != nil {
		b, err := json.Marshal(details)
		if err != nil {
			return "", nil, err
		}
		voData["data"] = string(b)
	}

	if routingKey == "" {
		routingKey = s.routingKey
	}

	// Post data to VO
	var post bytes.Buffer
	enc := json.NewEncoder(&post)
	err := enc.Encode(voData)
	if err != nil {
		return "", nil, err
	}
	return s.url + routingKey, &post, nil
}
