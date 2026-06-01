package bot

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"meshnode/autonotif/internal/meshtastic"
)

type fakeMesh struct {
	nodeNum     uint32
	longName    string
	replies     []reply
	acks        []ack
	publishErr  error
	ackErr      error
	subscribed  bool
	subscribeFn func(meshtastic.IncomingMessage)
}

type reply struct {
	to   uint32
	text string
}

type ack struct {
	to        uint32
	requestID uint32
}

func (m *fakeMesh) NodeNum() uint32  { return m.nodeNum }
func (m *fakeMesh) LongName() string { return m.longName }
func (m *fakeMesh) PublishTextTo(to uint32, text string) error {
	if m.publishErr != nil {
		return m.publishErr
	}
	m.replies = append(m.replies, reply{to: to, text: text})
	return nil
}
func (m *fakeMesh) AckDirectMessage(to uint32, requestID uint32) error {
	if m.ackErr != nil {
		return m.ackErr
	}
	m.acks = append(m.acks, ack{to: to, requestID: requestID})
	return nil
}
func (m *fakeMesh) SubscribeMessages(ctx context.Context, handler func(meshtastic.IncomingMessage)) error {
	m.subscribed = true
	m.subscribeFn = handler
	return nil
}

func TestResponderRepliesToDMCommand(t *testing.T) {
	mesh := &fakeMesh{nodeNum: 0x77727342, longName: "MeshNode WRS"}
	responder := NewResponder(Config{ReplyToDM: true}, mesh)

	responder.HandleMessage(meshtastic.IncomingMessage{From: 0x11111111, To: mesh.nodeNum, PacketID: 0xdeadbeef, Text: "/ping", IsDirect: true})

	if len(mesh.replies) != 1 {
		t.Fatalf("replies = %d, want 1", len(mesh.replies))
	}
	if mesh.replies[0].to != 0x11111111 || mesh.replies[0].text != "pong" {
		t.Fatalf("unexpected reply: %+v", mesh.replies[0])
	}
	if len(mesh.acks) != 1 {
		t.Fatalf("acks = %d, want 1", len(mesh.acks))
	}
	if mesh.acks[0].to != 0x11111111 || mesh.acks[0].requestID != 0xdeadbeef {
		t.Fatalf("unexpected ack: %+v", mesh.acks[0])
	}
}

func TestResponderIgnoresDMWithoutPrefix(t *testing.T) {
	mesh := &fakeMesh{nodeNum: 0x77727342, longName: "MeshNode WRS"}
	responder := NewResponder(Config{ReplyToDM: true, AdminNodes: []uint32{0xaf1e4204}}, mesh)

	responder.HandleMessage(meshtastic.IncomingMessage{From: 0x11111111, To: mesh.nodeNum, Text: "ping", IsDirect: true})
	responder.HandleMessage(meshtastic.IncomingMessage{From: 0xaf1e4204, To: mesh.nodeNum, Text: "ping", IsDirect: true})
	responder.HandleMessage(meshtastic.IncomingMessage{From: 0xaf1e4204, To: mesh.nodeNum, Text: "menu", IsDirect: true})

	if len(mesh.replies) != 0 {
		t.Fatalf("DM without prefix should be ignored, got replies: %+v", mesh.replies)
	}
}

func TestResponderIgnoresNonAdminDMWhenDisabled(t *testing.T) {
	mesh := &fakeMesh{nodeNum: 0x77727342, longName: "MeshNode WRS"}
	responder := NewResponder(Config{ReplyToDM: false, AdminNodes: []uint32{0xaf1e4204}}, mesh)

	responder.HandleMessage(meshtastic.IncomingMessage{From: 0x11111111, To: mesh.nodeNum, Text: "/ping", IsDirect: true})

	if len(mesh.replies) != 0 {
		t.Fatalf("non-admin DM should be ignored when ReplyToDM=false, got replies: %+v", mesh.replies)
	}
}

func TestResponderAdminBypassesDMDisabled(t *testing.T) {
	mesh := &fakeMesh{nodeNum: 0x77727342, longName: "MeshNode WRS"}
	responder := NewResponder(Config{ReplyToDM: false, AdminNodes: []uint32{0xaf1e4204}}, mesh)

	responder.HandleMessage(meshtastic.IncomingMessage{From: 0xaf1e4204, To: mesh.nodeNum, Text: "/ping", IsDirect: true})

	if len(mesh.replies) != 1 || mesh.replies[0].text != "pong" {
		t.Fatalf("admin should bypass disabled ReplyToDM, got replies: %+v", mesh.replies)
	}
}

func TestResponderAdminAlwaysBypassesDMDisabled(t *testing.T) {
	mesh := &fakeMesh{nodeNum: 0x77727342, longName: "MeshNode WRS"}
	responder := NewResponder(Config{ReplyToDM: false, AdminNodes: []uint32{0xaf1e4204}}, mesh)

	responder.HandleMessage(meshtastic.IncomingMessage{From: 0xaf1e4204, To: mesh.nodeNum, Text: "/ping", IsDirect: true})

	if len(mesh.replies) != 1 || mesh.replies[0].text != "pong" {
		t.Fatalf("admin should always bypass disabled ReplyToDM, got replies: %+v", mesh.replies)
	}
}

func TestResponderAdminConfigMenuAndReplyToDMToggle(t *testing.T) {
	mesh := &fakeMesh{nodeNum: 0x77727342, longName: "MeshNode WRS"}
	responder := NewResponder(Config{ReplyToDM: false, AdminNodes: []uint32{0xaf1e4204}, ConfigFile: filepath.Join(t.TempDir(), "bot-config.json")}, mesh)
	responder.Register(Command{Name: "gempa", Handler: func(context.Context, meshtastic.IncomingMessage) string { return "stub" }})

	responder.HandleMessage(meshtastic.IncomingMessage{From: 0xaf1e4204, To: mesh.nodeNum, Text: "/menu", IsDirect: true})
	responder.HandleMessage(meshtastic.IncomingMessage{From: 0xaf1e4204, To: mesh.nodeNum, Text: "/config", IsDirect: true})
	responder.HandleMessage(meshtastic.IncomingMessage{From: 0xaf1e4204, To: mesh.nodeNum, Text: "/config reply_to_dm on", IsDirect: true})
	responder.HandleMessage(meshtastic.IncomingMessage{From: 0x11111111, To: mesh.nodeNum, Text: "/ping", IsDirect: true})

	if len(mesh.replies) != 4 {
		t.Fatalf("replies = %+v, want 4", mesh.replies)
	}
	if mesh.replies[0].text != "Perintah: ping, status, gempa, config" {
		t.Fatalf("admin menu = %q", mesh.replies[0].text)
	}
	if mesh.replies[1].text != "Config:\nreply_to_dm: off\nunknown_command_reply: off\ncommand_prefixes: /,.\nUbah: config reply_to_dm on/off" {
		t.Fatalf("config menu = %q", mesh.replies[1].text)
	}
	if mesh.replies[2].text != "Config tersimpan: reply_to_dm on" {
		t.Fatalf("toggle reply = %q", mesh.replies[2].text)
	}
	if mesh.replies[3].text != "pong" {
		t.Fatalf("non-admin should be answered after toggle on, got %q", mesh.replies[3].text)
	}
}

func TestResponderIgnoresSelfAndNonDM(t *testing.T) {
	mesh := &fakeMesh{nodeNum: 0x77727342, longName: "MeshNode WRS"}
	responder := NewResponder(Config{ReplyToDM: true}, mesh)

	responder.HandleMessage(meshtastic.IncomingMessage{From: mesh.nodeNum, To: mesh.nodeNum, Text: "ping", IsDirect: true})
	responder.HandleMessage(meshtastic.IncomingMessage{From: 0x11111111, To: meshtastic.BroadcastNode, Text: "ping", IsBroadcast: true})

	if len(mesh.replies) != 0 {
		t.Fatalf("replies = %+v, want none", mesh.replies)
	}
}

func TestResponderIgnoresBroadcastMention(t *testing.T) {
	mesh := &fakeMesh{nodeNum: 0x77727342, longName: "MeshNode WRS"}
	responder := NewResponder(Config{ReplyToDM: true}, mesh)

	responder.HandleMessage(meshtastic.IncomingMessage{From: 0x11111111, To: meshtastic.BroadcastNode, Text: "bot status", IsBroadcast: true})

	if len(mesh.replies) != 0 {
		t.Fatalf("broadcast mention should be ignored, got replies: %+v", mesh.replies)
	}
}

func TestResponderIgnoresUnknownCommand(t *testing.T) {
	mesh := &fakeMesh{nodeNum: 0x77727342, longName: "MeshNode WRS"}
	responder := NewResponder(Config{ReplyToDM: true}, mesh)

	responder.HandleMessage(meshtastic.IncomingMessage{From: 0x11111111, To: mesh.nodeNum, Text: "ngopi", IsDirect: true})

	if len(mesh.replies) != 0 {
		t.Fatalf("unknown command should be ignored, got replies: %+v", mesh.replies)
	}
}

func TestResponderAcceptsSlashAndDotPrefixes(t *testing.T) {
	mesh := &fakeMesh{nodeNum: 0x77727342, longName: "MeshNode WRS"}
	responder := NewResponder(Config{ReplyToDM: true}, mesh)

	responder.HandleMessage(meshtastic.IncomingMessage{From: 0x11111111, To: mesh.nodeNum, Text: "/ping", IsDirect: true})
	responder.HandleMessage(meshtastic.IncomingMessage{From: 0x11111111, To: mesh.nodeNum, Text: ".status", IsDirect: true})

	if len(mesh.replies) != 2 {
		t.Fatalf("replies = %+v, want 2", mesh.replies)
	}
	if mesh.replies[0].text != "pong" {
		t.Fatalf("slash ping reply = %q", mesh.replies[0].text)
	}
	if mesh.replies[1].text != "MeshNode WRS aktif sebagai !77727342" {
		t.Fatalf("dot status reply = %q", mesh.replies[1].text)
	}
}

func TestResponderStartSubscribes(t *testing.T) {
	mesh := &fakeMesh{nodeNum: 0x77727342, longName: "MeshNode WRS"}
	responder := NewResponder(Config{}, mesh)
	if err := responder.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if !mesh.subscribed || mesh.subscribeFn == nil {
		t.Fatal("Start() did not subscribe handler")
	}
}

func TestResponderPublishErrorDoesNotPanic(t *testing.T) {
	mesh := &fakeMesh{nodeNum: 0x77727342, longName: "MeshNode WRS", publishErr: errors.New("mqtt down")}
	responder := NewResponder(Config{ReplyToDM: true}, mesh)

	responder.HandleMessage(meshtastic.IncomingMessage{From: 0x11111111, To: mesh.nodeNum, Text: "/ping", IsDirect: true})

	if len(mesh.replies) != 0 {
		t.Fatalf("reply should not be recorded on publish error: %+v", mesh.replies)
	}
}

func TestResponderRegisterCustomCommand(t *testing.T) {
	mesh := &fakeMesh{nodeNum: 0x77727342, longName: "MeshNode WRS"}
	responder := NewResponder(Config{ReplyToDM: true}, mesh)

	called := 0
	responder.Register(Command{
		Name: "gempa",
		Handler: func(ctx context.Context, msg meshtastic.IncomingMessage) string {
			called++
			return "stub-reply"
		},
	})

	responder.HandleMessage(meshtastic.IncomingMessage{From: 0x11111111, To: mesh.nodeNum, Text: ".gempa", IsDirect: true})
	responder.HandleMessage(meshtastic.IncomingMessage{From: 0x11111111, To: mesh.nodeNum, Text: "/gempa", IsDirect: true})

	if called != 2 {
		t.Fatalf("custom handler called %d times, want 2", called)
	}
	if len(mesh.replies) != 2 || mesh.replies[0].text != "stub-reply" || mesh.replies[1].text != "stub-reply" {
		t.Fatalf("custom handler replies = %+v", mesh.replies)
	}

	// help text should include the registered command
	responder.HandleMessage(meshtastic.IncomingMessage{From: 0x11111111, To: mesh.nodeNum, Text: "/help", IsDirect: true})
	if len(mesh.replies) != 3 || mesh.replies[2].text != "Perintah: ping, status, gempa" {
		t.Fatalf("help text = %q", mesh.replies[2].text)
	}
}

func TestResponderAdminOnlyCustomCommand(t *testing.T) {
	mesh := &fakeMesh{nodeNum: 0x77727342, longName: "MeshNode WRS"}
	responder := NewResponder(Config{ReplyToDM: true, AdminNodes: []uint32{0xaf1e4204}}, mesh)

	called := 0
	responder.Register(Command{
		Name:      "restart",
		AdminOnly: true,
		Handler: func(ctx context.Context, msg meshtastic.IncomingMessage) string {
			called++
			return "ok"
		},
	})

	// non-admin: silently ignored (UnknownCommandReply default off)
	responder.HandleMessage(meshtastic.IncomingMessage{From: 0x11111111, To: mesh.nodeNum, Text: "/restart", IsDirect: true})
	if called != 0 || len(mesh.replies) != 0 {
		t.Fatalf("non-admin should not invoke admin command: called=%d replies=%+v", called, mesh.replies)
	}

	// admin: invoked
	responder.HandleMessage(meshtastic.IncomingMessage{From: 0xaf1e4204, To: mesh.nodeNum, Text: "/restart", IsDirect: true})
	if called != 1 || len(mesh.replies) != 1 || mesh.replies[0].text != "ok" {
		t.Fatalf("admin command not invoked: called=%d replies=%+v", called, mesh.replies)
	}

	// admin help text contains the admin-only command; non-admin help does not
	responder.HandleMessage(meshtastic.IncomingMessage{From: 0xaf1e4204, To: mesh.nodeNum, Text: "/help", IsDirect: true})
	if len(mesh.replies) != 2 || mesh.replies[1].text != "Perintah: ping, status, restart, config" {
		t.Fatalf("admin help = %q", mesh.replies[1].text)
	}
	responder.HandleMessage(meshtastic.IncomingMessage{From: 0x11111111, To: mesh.nodeNum, Text: "/help", IsDirect: true})
	if len(mesh.replies) != 3 || mesh.replies[2].text != "Perintah: ping, status" {
		t.Fatalf("non-admin help = %q", mesh.replies[2].text)
	}
}
