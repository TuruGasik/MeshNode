package bot

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"meshnode/autonotif/internal/meshtastic"
	"meshnode/autonotif/internal/util"
)

// Mesh is the subset of the Meshtastic client used by the bot.
type Mesh interface {
	NodeNum() uint32
	LongName() string
	PublishTextTo(to uint32, text string) error
	AckDirectMessage(to uint32, requestID uint32) error
	SubscribeMessages(ctx context.Context, handler func(meshtastic.IncomingMessage)) error
}

// CommandHandler computes a reply for an incoming command. Empty string means
// no reply is sent.
type CommandHandler func(ctx context.Context, msg meshtastic.IncomingMessage) string

// Command is a user-extensible bot command. Name must be lowercase and is
// matched literally after prefix stripping. AdminOnly commands are only
// invoked when the sender is in cfg.AdminNodes.
type Command struct {
	Name      string
	AdminOnly bool
	Handler   CommandHandler
}

type Responder struct {
	cfg      Config
	mesh     Mesh
	runtime  RuntimeConfig
	commands map[string]Command
	order    []string
}

func NewResponder(cfg Config, mesh Mesh) *Responder {
	runtime, err := LoadRuntimeConfig(cfg.ConfigFile, DefaultRuntimeConfig(cfg.ReplyToDM))
	if err != nil {
		slog.Warn("bot config load failed", "file", cfg.ConfigFile, "error", err)
		runtime = DefaultRuntimeConfig(cfg.ReplyToDM)
	} else if strings.TrimSpace(cfg.ConfigFile) != "" {
		slog.Info("loaded bot config", "file", cfg.ConfigFile, "reply_to_dm", runtime.ReplyToDM)
	}
	return &Responder{
		cfg:      cfg,
		mesh:     mesh,
		runtime:  runtime,
		commands: map[string]Command{},
	}
}

// Register adds a custom command. Duplicate names overwrite the previous
// handler but keep the original position in help text.
func (r *Responder) Register(cmd Command) {
	name := strings.ToLower(strings.TrimSpace(cmd.Name))
	if name == "" || cmd.Handler == nil {
		return
	}
	cmd.Name = name
	if _, exists := r.commands[name]; !exists {
		r.order = append(r.order, name)
	}
	r.commands[name] = cmd
}

func (r *Responder) Start(ctx context.Context) error {
	return r.mesh.SubscribeMessages(ctx, r.HandleMessage)
}

func (r *Responder) HandleMessage(msg meshtastic.IncomingMessage) {
	if !r.shouldRespond(msg) {
		return
	}

	// Send a Routing ACK as soon as possible so the sender's device stops
	// retransmitting the DM. This is fire-and-forget — failures are logged
	// but do not abort the reply path.
	if msg.IsDirect && msg.PacketID != 0 {
		if err := r.mesh.AckDirectMessage(msg.From, msg.PacketID); err != nil {
			slog.Warn("ack failed",
				"to", meshtastic.FormatNodeID(msg.From),
				"packet_id", msg.PacketID,
				"error", err,
			)
		} else {
			slog.Debug("ack sent",
				"to", meshtastic.FormatNodeID(msg.From),
				"packet_id", msg.PacketID,
			)
		}
	}

	reply := r.route(context.Background(), msg)
	if strings.TrimSpace(reply) == "" {
		slog.Debug("direct message ignored without reply", "from", meshtastic.FormatNodeID(msg.From), "packet_id", msg.PacketID)
		return
	}
	reply = util.TrimRunes(reply, 230)

	if err := r.mesh.PublishTextTo(msg.From, reply); err != nil {
		slog.Warn("direct reply failed", "to", meshtastic.FormatNodeID(msg.From), "error", err)
		return
	}
	slog.Info("direct reply sent", "to", meshtastic.FormatNodeID(msg.From), "packet_id", msg.PacketID, "reply", reply)
}

func (r *Responder) shouldRespond(msg meshtastic.IncomingMessage) bool {
	if msg.From == r.mesh.NodeNum() {
		return false
	}
	if !msg.IsDirect {
		return false
	}
	if r.isAdmin(msg.From) {
		return true
	}
	return r.runtime.ReplyToDM
}

func (r *Responder) isAdmin(nodeID uint32) bool {
	for _, adminNode := range r.cfg.AdminNodes {
		if nodeID == adminNode {
			return true
		}
	}
	return false
}

func (r *Responder) route(ctx context.Context, msg meshtastic.IncomingMessage) string {
	cmd, ok := r.normalizeCommand(msg.Text)
	if !ok {
		return ""
	}
	isAdmin := r.isAdmin(msg.From)

	switch cmd {
	case "", "help", "menu":
		return r.helpText(isAdmin)
	case "ping":
		return "pong"
	case "status":
		return fmt.Sprintf("%s aktif sebagai %s", r.mesh.LongName(), meshtastic.FormatNodeID(r.mesh.NodeNum()))
	case "config":
		if !isAdmin {
			return r.unknownReply()
		}
		return r.configMenu()
	case "config reply_to_dm on", "config reply dm on", "config reply on", "reply_to_dm on", "reply dm on":
		if !isAdmin {
			return r.unknownReply()
		}
		return r.setReplyToDM(true)
	case "config reply_to_dm off", "config reply dm off", "config reply off", "reply_to_dm off", "reply dm off":
		if !isAdmin {
			return r.unknownReply()
		}
		return r.setReplyToDM(false)
	}

	if registered, ok := r.commands[cmd]; ok {
		if registered.AdminOnly && !isAdmin {
			return r.unknownReply()
		}
		return registered.Handler(ctx, msg)
	}

	return r.unknownReply()
}

func (r *Responder) unknownReply() string {
	if r.runtime.UnknownCommandReply {
		return "Perintah tidak dikenal. Ketik help"
	}
	return ""
}

func (r *Responder) helpText(isAdmin bool) string {
	parts := []string{"ping", "status"}
	for _, name := range r.order {
		cmd := r.commands[name]
		if cmd.AdminOnly && !isAdmin {
			continue
		}
		parts = append(parts, name)
	}
	if isAdmin {
		parts = append(parts, "config")
	}
	return "Perintah: " + strings.Join(parts, ", ")
}

// normalizeCommand returns the lowercased command body when text starts with
// one of the configured prefixes. Messages without a prefix are ignored so the
// bot stays silent on regular conversation. When no prefixes are configured,
// every non-empty message is treated as a command (legacy behaviour).
func (r *Responder) normalizeCommand(text string) (string, bool) {
	cmd := strings.TrimSpace(text)
	if cmd == "" {
		return "", false
	}
	prefixes := r.activePrefixes()
	if len(prefixes) == 0 {
		return strings.ToLower(cmd), true
	}
	for _, prefix := range prefixes {
		if !strings.HasPrefix(cmd, prefix) {
			continue
		}
		cmd = strings.TrimSpace(strings.TrimPrefix(cmd, prefix))
		if cmd == "" {
			return "", true
		}
		return strings.ToLower(cmd), true
	}
	return "", false
}

func (r *Responder) activePrefixes() []string {
	out := make([]string, 0, len(r.runtime.CommandPrefixes))
	for _, p := range r.runtime.CommandPrefixes {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (r *Responder) configMenu() string {
	status := "off"
	if r.runtime.ReplyToDM {
		status = "on"
	}
	unknownStatus := "off"
	if r.runtime.UnknownCommandReply {
		unknownStatus = "on"
	}
	return fmt.Sprintf("Config:\nreply_to_dm: %s\nunknown_command_reply: %s\ncommand_prefixes: %s\nUbah: config reply_to_dm on/off", status, unknownStatus, strings.Join(r.runtime.CommandPrefixes, ","))
}

func (r *Responder) setReplyToDM(enabled bool) string {
	r.runtime.ReplyToDM = enabled
	if err := SaveRuntimeConfig(r.cfg.ConfigFile, r.runtime); err != nil {
		slog.Warn("bot config save failed", "file", r.cfg.ConfigFile, "error", err)
		return "Config gagal disimpan"
	}
	if enabled {
		return "Config tersimpan: reply_to_dm on"
	}
	return "Config tersimpan: reply_to_dm off"
}
