package discord

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSendAndEdit(t *testing.T) {
	var mu sync.Mutex
	var requests []struct {
		method string
		path   string
		query  string
		body   map[string]any
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		data, _ := io.ReadAll(request.Body)
		var body map[string]any
		_ = json.Unmarshal(data, &body)

		mu.Lock()
		requests = append(requests, struct {
			method string
			path   string
			query  string
			body   map[string]any
		}{request.Method, request.URL.Path, request.URL.RawQuery, body})
		mu.Unlock()

		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPost {
			_, _ = writer.Write([]byte(`{"id":"111","channel_id":"222"}`))
			return
		}
		_, _ = writer.Write([]byte(`{"id":"111","channel_id":"222","edited":true}`))
	}))
	defer server.Close()

	webhook := New(server.URL, "Bot", "https://example.com/avatar.png", 0, 5*time.Second, nil, false)
	result, err := webhook.Send(map[string]any{"content": "hola"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if result["id"] != "111" {
		t.Fatalf("ID inesperado: %+v", result)
	}

	mu.Lock()
	first := requests[0]
	mu.Unlock()
	if first.method != http.MethodPost || first.query != "wait=true" {
		t.Fatalf("petición POST inesperada: %+v", first)
	}
	if first.body["username"] != "Bot" || first.body["avatar_url"] != "https://example.com/avatar.png" {
		t.Fatalf("faltan username/avatar_url: %+v", first.body)
	}

	if _, err := webhook.Edit("111", map[string]any{"content": "editado"}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	second := requests[1]
	mu.Unlock()
	if second.method != http.MethodPatch || !strings.HasSuffix(second.path, "/messages/111") {
		t.Fatalf("petición PATCH inesperada: %+v", second)
	}
	if second.body["content"] != "editado" {
		t.Fatalf("cuerpo de edición inesperado: %+v", second.body)
	}
}

func TestRetryWithoutComponents(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	sawComponents := false

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		data, _ := io.ReadAll(request.Body)
		var body map[string]any
		_ = json.Unmarshal(data, &body)

		mu.Lock()
		attempts++
		attempt := attempts
		_, hasComponents := body["components"]
		if hasComponents {
			sawComponents = true
		}
		mu.Unlock()

		if attempt == 1 {
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = writer.Write([]byte(`{"message":"invalid components"}`))
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"333"}`))
	}))
	defer server.Close()

	webhook := New(server.URL, "", "", 0, 5*time.Second, nil, false)
	result, err := webhook.Send(map[string]any{
		"content":    "con botones",
		"components": []any{map[string]any{"type": 1}},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if result["id"] != "333" {
		t.Fatalf("ID inesperado: %+v", result)
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 2 {
		t.Fatalf("se esperaban 2 intentos, hubo %d", attempts)
	}
	if !sawComponents {
		t.Fatal("el primer intento debía incluir componentes")
	}
}

func TestValidateAndHTTPError(t *testing.T) {
	notFound := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
		_, _ = writer.Write([]byte(`{"message":"Unknown Webhook"}`))
	}))
	defer notFound.Close()

	webhook := New(notFound.URL, "", "", 0, 5*time.Second, nil, false)
	if _, err := webhook.Validate(); err == nil || !strings.Contains(err.Error(), "no existe") {
		t.Fatalf("validación inesperada: %v", err)
	}

	badRequest := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"message":"bad payload"}`))
	}))
	defer badRequest.Close()

	webhook = New(badRequest.URL, "", "", 0, 5*time.Second, nil, false)
	_, err := webhook.Send(map[string]any{"content": "x"}, false)
	if err == nil {
		t.Fatal("se esperaba un error")
	}
	discordErr, ok := err.(*Error)
	if !ok || discordErr.Status != 400 {
		t.Fatalf("error inesperado: %#v", err)
	}
}

func TestDryRun(t *testing.T) {
	webhook := New("http://127.0.0.1:1/no-usar", "", "", 0, time.Second, nil, true)
	result, err := webhook.Send(map[string]any{"content": "hola"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if result["dryRun"] != true || result["id"] != "dry-run" {
		t.Fatalf("resultado dry-run inesperado: %+v", result)
	}
}
