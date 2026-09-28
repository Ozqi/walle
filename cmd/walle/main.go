// Command walle 提供 CLI、daemon、ps 和 attach 入口。
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/Ozqi/walle/internal/cli"
	"github.com/Ozqi/walle/internal/daemon"
	"github.com/Ozqi/walle/internal/tui"
	"github.com/Ozqi/walle/internal/utils"
	"github.com/spf13/cobra"
)

var debugMode bool
var sessionID string
var continueLast bool
var llmFormat string
var llmModel string
var modelRef string
var daemonHTTP bool
var daemonHTTPAddr string
var daemonHTTPToken string
var daemonHTTPOrigins []string
var daemonAllowNonLoopback bool

func main() {
	rootCmd := &cobra.Command{Use: "walle", Short: "walle - A lightweight AI agent runtime", Run: runTUI}
	rootCmd.CompletionOptions.DisableDefaultCmd = true
	rootCmd.PersistentFlags().BoolVar(&debugMode, "debug", false, "Enable debug mode with verbose logging")
	rootCmd.PersistentFlags().StringVar(&sessionID, "session", "", "Resume from existing session ID")
	rootCmd.PersistentFlags().BoolVarP(&continueLast, "continue", "c", false, "Resume from the last session")
	rootCmd.PersistentFlags().StringVar(&llmFormat, "llm-format", "", "Temporarily select LLM API format: claude or openai")
	rootCmd.PersistentFlags().StringVar(&llmModel, "llm-model", "", "Temporarily override the selected LLM model")
	rootCmd.PersistentFlags().StringVarP(&modelRef, "model", "m", "", "Temporarily select model as provider/model")
	daemonCmd := &cobra.Command{Use: "daemon", Short: "Host attachable interactive Agents", Run: runDaemon}
	daemonCmd.Flags().BoolVar(&daemonHTTP, "http", false, "Enable localhost HTTP/WebSocket/SSE gateway")
	daemonCmd.Flags().StringVar(&daemonHTTPAddr, "http-addr", "127.0.0.1:0", "HTTP gateway listen address")
	daemonCmd.Flags().StringVar(&daemonHTTPToken, "http-token", "", "HTTP gateway bearer token; defaults to ~/.walle/run/http_token")
	daemonCmd.Flags().StringArrayVar(&daemonHTTPOrigins, "allow-origin", nil, "Additional allowed browser Origin for HTTP gateway")
	daemonCmd.Flags().BoolVar(&daemonAllowNonLoopback, "allow-non-loopback", false, "Allow HTTP gateway to listen on non-loopback addresses")
	rootCmd.AddCommand(newPSCommand(), newAttachCommand(), daemonCmd)
	if err := rootCmd.Execute(); err != nil {
		cli.PrintError(err)
		os.Exit(1)
	}
}

func runTUI(cmd *cobra.Command, _ []string) {
	client, err := startInteractiveClient(cmd.Context())
	if err != nil {
		cli.PrintError(err)
		os.Exit(1)
	}
	if err := tui.LaunchAttachedTUI(cmd.Context(), client); err != nil {
		cli.PrintError(fmt.Errorf("tui error: %w", err))
		os.Exit(1)
	}
}

func runDaemon(_ *cobra.Command, _ []string) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	configDir, err := utils.GetConfigDir()
	if err != nil {
		cli.PrintError(err)
		return
	}
	registry := daemon.NewRegistry(ctx)
	defer registry.Close()
	runDir := filepath.Join(configDir, "run")
	control, err := daemon.StartControlServer(ctx, runDir, registry)
	if err != nil {
		cli.PrintError(err)
		return
	}
	defer control.Close()
	fmt.Println("interactive supervisor ready")
	if daemonHTTP {
		httpGateway, err := daemon.StartHTTPServer(ctx, runDir, registry, daemon.HTTPOptions{
			Addr: daemonHTTPAddr, Token: daemonHTTPToken, AllowedOrigins: daemonHTTPOrigins,
			AllowNonLoopback: daemonAllowNonLoopback,
		})
		if err != nil {
			cli.PrintError(err)
			return
		}
		defer httpGateway.Close()
		if daemonAllowNonLoopback {
			fmt.Println("warning: http gateway accepts non-loopback connections")
		}
		fmt.Printf("http gateway ready at %s\n", httpGateway.Addr())
	}
	<-ctx.Done()
}
