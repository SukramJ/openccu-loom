// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"testing"
)

func TestRouterRegisterAndDispatchSuccess(t *testing.T) {
	r := NewRouter()
	r.Register("system.health", func(_ context.Context, args json.RawMessage) (any, error) {
		return map[string]any{"ok": true, "args": string(args)}, nil
	})
	if !r.Has("system.health") {
		t.Fatal("Has must report true after Register")
	}
	res := r.Dispatch(context.Background(), "system.health", json.RawMessage(`{"x":1}`))
	if res.Error != nil {
		t.Fatalf("unexpected error: %+v", res.Error)
	}
	m, ok := res.Data.(map[string]any)
	if !ok || m["ok"] != true {
		t.Fatalf("data=%+v want {ok:true}", res.Data)
	}
	if m["args"] != `{"x":1}` {
		t.Fatalf("args=%v want raw {x:1}", m["args"])
	}
}

func TestRouterUnknownCommand(t *testing.T) {
	r := NewRouter()
	res := r.Dispatch(context.Background(), "ghost", nil)
	if res.Error == nil {
		t.Fatal("unknown command must produce an error")
	}
	if res.Error.Code != CommandErrorUnknownCommand {
		t.Fatalf("code=%q want %q", res.Error.Code, CommandErrorUnknownCommand)
	}
}

func TestRouterCommandHandlerErrorWrapped(t *testing.T) {
	r := NewRouter()
	r.Register("fails", func(context.Context, json.RawMessage) (any, error) {
		return nil, errors.New("boom")
	})
	res := r.Dispatch(context.Background(), "fails", nil)
	if res.Error == nil || res.Error.Code != CommandErrorInternal {
		t.Fatalf("got %+v want internal_error", res.Error)
	}
	if res.Error.Message != "boom" {
		t.Fatalf("message=%q want boom", res.Error.Message)
	}
}

func TestRouterCommandHandlerTypedError(t *testing.T) {
	r := NewRouter()
	r.Register("denied", func(context.Context, json.RawMessage) (any, error) {
		return nil, NewCommandError(CommandErrorUnauthorized, "no token")
	})
	res := r.Dispatch(context.Background(), "denied", nil)
	if res.Error == nil || res.Error.Code != CommandErrorUnauthorized {
		t.Fatalf("got %+v want unauthorized", res.Error)
	}
}

func TestRouterCommandsListsRegistered(t *testing.T) {
	r := NewRouter()
	r.Register("a", func(context.Context, json.RawMessage) (any, error) { return nil, nil })
	r.Register("b", func(context.Context, json.RawMessage) (any, error) { return nil, nil })
	r.Register("c", func(context.Context, json.RawMessage) (any, error) { return nil, nil })
	got := r.Commands()
	sort.Strings(got)
	want := []string{"a", "b", "c"}
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("Commands=%v want %v", got, want)
	}
}

func TestRouterReplaceHandler(t *testing.T) {
	r := NewRouter()
	r.Register("x", func(context.Context, json.RawMessage) (any, error) { return "first", nil })
	r.Register("x", func(context.Context, json.RawMessage) (any, error) { return "second", nil })
	res := r.Dispatch(context.Background(), "x", nil)
	if res.Error != nil {
		t.Fatal(res.Error)
	}
	if res.Data != "second" {
		t.Fatalf("data=%v want second", res.Data)
	}
}

func TestRouterIgnoresZeroValues(t *testing.T) {
	var r *Router
	r.Register("x", func(context.Context, json.RawMessage) (any, error) { return nil, nil })
	r2 := NewRouter()
	r2.Register("", func(context.Context, json.RawMessage) (any, error) { return nil, nil })
	r2.Register("y", nil)
	if r2.Has("") || r2.Has("y") {
		t.Fatal("empty name and nil handler must be rejected")
	}
}

func TestHubExposesRouter(t *testing.T) {
	h := NewHub()
	if h.Router() == nil {
		t.Fatal("Hub.Router() must not be nil")
	}
}
