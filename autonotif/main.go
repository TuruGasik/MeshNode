package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"meshnode/autonotif/internal/bmkg"
	"meshnode/autonotif/internal/bot"
	"meshnode/autonotif/internal/config"
	"meshnode/autonotif/internal/hantavirus"
	"meshnode/autonotif/internal/meshtastic"
	"meshnode/autonotif/internal/util"
)

func main() {
	util.SetupLogger()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, config.Load()); err != nil {
		slog.Error("autonotif stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg config.Config) error {
	if cfg.Hantavirus.Once {
		return hantavirus.RunOnce(ctx, cfg.Hantavirus)
	}

	if cfg.Message != "" && cfg.DryRun {
		msg := util.TrimRunes(cfg.Message, 230)
		slog.Info("custom message prepared", "message", msg)
		fmt.Println(msg)
		return nil
	}

	mesh, err := meshtastic.NewClient(cfg.Meshtastic)
	if err != nil {
		return err
	}

	if !cfg.DryRun {
		if err := mesh.Connect(); err != nil {
			return err
		}
		defer mesh.Disconnect()
		if err := mesh.PublishNodeInfo(); err != nil {
			slog.Warn("startup node info publish failed", "error", err)
		} else {
			slog.Info("startup node info published")
		}
		meshtastic.StartNodeInfoPublisher(ctx, mesh, cfg.Meshtastic.NodeInfoInterval)
	}

	slog.Info("autonotif started",
		"dry_run", cfg.DryRun,
		"once", cfg.BMKG.Once,
		"send_on_start", cfg.BMKG.SendOnStart,
		"custom_message", cfg.Message != "",
		"bmkg_source", cfg.BMKG.Source,
		"bmkg_min_magnitude", cfg.BMKG.MinMagnitude,
		"bmkg_url", cfg.BMKG.URL,
		"bmkg_inatews2_url", cfg.BMKG.Inatews2URL,
		"poll_interval", cfg.BMKG.PollInterval,
		"state_file", cfg.BMKG.StateFile,
		"mqtt", fmt.Sprintf("%s:%d", cfg.Meshtastic.BrokerHost, cfg.Meshtastic.BrokerPort),
		"topic", mesh.PublishTopic(),
		"from_node", meshtastic.FormatNodeID(cfg.Meshtastic.FromNode),
		"long_name", cfg.Meshtastic.NodeLongName,
		"short_name", cfg.Meshtastic.NodeShortName,
		"node_info_interval", cfg.Meshtastic.NodeInfoInterval,
		"responder", cfg.Bot.EnableResponder,
	)

	if cfg.Message != "" {
		msg := util.TrimRunes(cfg.Message, 230)
		slog.Info("custom message prepared", "message", msg)
		if cfg.DryRun {
			fmt.Println(msg)
			return nil
		}
		if err := mesh.PublishText(msg); err != nil {
			return err
		}
		slog.Info("custom message sent", "message", msg)
		return nil
	}

	if !cfg.DryRun && cfg.Bot.EnableResponder {
		responder := bot.NewResponder(cfg.Bot, mesh)
		responder.Register(bot.Command{
			Name: "gempa",
			Handler: func(ctx context.Context, _ meshtastic.IncomingMessage) string {
				fetcher, err := bmkg.NewLatestFetcher(cfg.BMKG)
				if err != nil {
					return "Gagal konfigurasi sumber BMKG"
				}
				gempa, err := fetcher.Latest(ctx)
				if err != nil {
					return "Gagal ambil data BMKG terbaru"
				}
				return gempa.Message()
			},
		})
		if err := responder.Start(ctx); err != nil {
			return err
		}
	}

	bmkgNotifier, err := bmkg.NewNotifier(cfg.BMKG, mesh, cfg.DryRun)
	if err != nil {
		return err
	}
	return bmkgNotifier.Run(ctx)
}
