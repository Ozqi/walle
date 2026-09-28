package main

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/Ozqi/walle/internal/daemon"
	"github.com/Ozqi/walle/internal/tui"
	"github.com/Ozqi/walle/internal/utils"
	"github.com/spf13/cobra"
)

func newPSCommand() *cobra.Command {
	return &cobra.Command{Use: "ps", Short: "List running Agent processes", RunE: runPS}
}

func newAttachCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attach <process-id>",
		Short: "Follow a running Agent process",
		Args:  cobra.ExactArgs(1),
		RunE:  runAttach,
	}
	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		processes, err := runningProcesses()
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		var matches []string
		for _, proc := range processes {
			if strings.HasPrefix(proc.ID, prefix) {
				matches = append(matches, proc.ID+"\t"+processLabel(proc))
			}
		}
		return matches, cobra.ShellCompDirectiveNoFileComp
	}
	return cmd
}

func runPS(cmd *cobra.Command, args []string) error {
	processes, err := runningProcesses()
	if err != nil {
		return err
	}
	if len(processes) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No running Agent processes.")
		return nil
	}
	writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "PROCESS\tSTATE\tNAME\tSTARTED\tMODEL\tSESSION\tWORKSPACE")
	for _, proc := range processes {
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", proc.ID, proc.State, processLabel(proc), proc.StartedAt.Local().Format("15:04:05"), emptyDash(proc.Model), emptyDash(proc.SessionID), proc.Workspace)
	}
	return writer.Flush()
}

func runAttach(cmd *cobra.Command, args []string) error {
	// 先从控制目录解析目标进程，再按交互能力选择 Socket 或文件跟随通道。
	processes, err := runningProcesses()
	if err != nil {
		return err
	}
	for _, proc := range processes {
		if proc.ID != args[0] {
			continue
		}
		configDir, err := utils.GetConfigDir()
		if err != nil {
			return err
		}
		client, err := daemon.AttachProcess(configDir+"/run", proc.ID)
		if err != nil {
			return err
		}
		return tui.LaunchAttachedTUI(cmd.Context(), client)
	}
	return fmt.Errorf("running process %q not found", args[0])
}

func runningProcesses() ([]daemon.ProcessSnapshot, error) {
	configDir, err := utils.GetConfigDir()
	if err != nil {
		return nil, err
	}
	return daemon.ListProcesses(configDir + "/run")
}

func processLabel(proc daemon.ProcessSnapshot) string {
	if proc.Name != "" {
		return proc.Name
	}
	return "-"
}

func emptyDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
