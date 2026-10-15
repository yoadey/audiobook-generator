// Package llmtest provides a fake OpenAI-compatible chat completions server.
package llmtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"sync"
	"testing"
)

// Reply is one scripted answer.
type Reply struct {
	// Status defaults to 200.
	Status int
	Header map[string]string
	// Content is the message content of a 200 answer; Body replaces the
	// whole response body (for errors).
	Content      string
	Body         string
	FinishReason string
}

// Request is a received chat completion request.
type Request struct {
	Header http.Header
	Body   map[string]any
}

// Messages returns the request's messages.
func (r Request) Messages() []map[string]any {
	raw, _ := r.Body["messages"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, m := range raw {
		if mm, ok := m.(map[string]any); ok {
			out = append(out, mm)
		}
	}
	return out
}

// Server is the fake. Without scripted replies it answers the
// provider_test prompt correctly and everything else with "{}".
type Server struct {
	*httptest.Server
	Models []string
	// Respond, when set, answers requests no scripted reply is queued for.
	Respond func(Request) Reply

	mu         sync.Mutex
	replies    []Reply
	requests   []Request
	modelsAuth []string
}

// New starts a fake server that is closed when the test ends.
func New(t testing.TB) *Server {
	s := &Server{Models: []string{"fake-small", "fake-large"}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", s.chat)
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.modelsAuth = append(s.modelsAuth, r.Header.Get("Authorization"))
		s.mu.Unlock()
		data := make([]map[string]string, 0, len(s.Models))
		for _, m := range s.Models {
			data = append(data, map[string]string{"id": m, "object": "model"})
		}
		writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

// BaseURL is the URL to configure as provider base URL.
func (s *Server) BaseURL() string { return s.URL + "/v1/" }

// Enqueue scripts the next answers.
func (s *Server) Enqueue(r ...Reply) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replies = append(s.replies, r...)
}

// Requests returns the received requests.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// ModelsAuthorization returns the Authorization headers of the model
// list requests received.
func (s *Server) ModelsAuthorization() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.modelsAuth...)
}

var (
	addRe  = regexp.MustCompile(`Add (\d+) and (\d+)`)
	wordRe = regexp.MustCompile(`exactly this word: (\w+)`)
)

func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	req := Request{Header: r.Header.Clone(), Body: body}
	s.mu.Lock()
	s.requests = append(s.requests, req)
	var reply *Reply
	if len(s.replies) > 0 {
		reply = &s.replies[0]
		s.replies = s.replies[1:]
	}
	s.mu.Unlock()

	if reply == nil && s.Respond != nil {
		r := s.Respond(req)
		reply = &r
	}
	if reply == nil {
		reply = &Reply{Content: autoAnswer(req)}
	}
	for k, v := range reply.Header {
		w.Header().Set(k, v)
	}
	status := reply.Status
	if status == 0 {
		status = http.StatusOK
	}
	if reply.Body != "" || status != http.StatusOK {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply.Body))
		return
	}
	finish := reply.FinishReason
	if finish == "" {
		finish = "stop"
	}
	model, _ := body["model"].(string)
	in := 0
	for _, m := range req.Messages() {
		c, _ := m["content"].(string)
		in += len(c) / 4
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":     "chatcmpl-fake",
		"object": "chat.completion",
		"model":  model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": reply.Content},
			"finish_reason": finish,
		}},
		"usage": map[string]int{"prompt_tokens": in, "completion_tokens": len(reply.Content) / 4, "total_tokens": in + len(reply.Content)/4},
	})
}

// autoAnswer solves the provider_test prompt.
func autoAnswer(req Request) string {
	for _, m := range req.Messages() {
		c, _ := m["content"].(string)
		add, word := addRe.FindStringSubmatch(c), wordRe.FindStringSubmatch(c)
		if add != nil && word != nil {
			a, _ := strconv.Atoi(add[1])
			b, _ := strconv.Atoi(add[2])
			return fmt.Sprintf(`{"sum": %d, "echo": %q}`, a+b, word[1])
		}
	}
	return "{}"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
