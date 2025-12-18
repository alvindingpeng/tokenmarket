package cmd

import (
	"github.com/alvindingpeng/switchyard/internal/conf"
	"github.com/alvindingpeng/switchyard/internal/db"
	"github.com/alvindingpeng/switchyard/internal/op"
	"github.com/alvindingpeng/switchyard/internal/server"
	"github.com/alvindingpeng/switchyard/internal/task"
	"github.com/alvindingpeng/switchyard/internal/utils/log"
	"github.com/alvindingpeng/switchyard/internal/utils/shutdown"
	"github.com/spf13/cobra"
)

var cfgFile string

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start " + conf.APP_NAME,
	PreRun: func(cmd *cobra.Command, args []string) {
		conf.PrintBanner()
		conf.Load(cfgFile)
	},
	Run: func(cmd *cobra.Command, args []string) {
		shutdown.Init(log.Logger)
		defer shutdown.Listen()
		if err := db.InitDB(conf.AppConfig.Database.Path, conf.IsDebug()); err != nil {
			log.Errorf("database init error: %v", err)
			return
		}
		shutdown.Register(db.Close)

		if err := op.InitCache(); err != nil {
			log.Errorf("cache init error: %v", err)
			return
		}
		shutdown.Register(op.SaveCache)

		if err := op.UserInit(); err != nil {
			log.Errorf("user init error: %v", err)
			return
		}

		if err := server.Start(); err != nil {
			log.Errorf("server start error: %v", err)
			return
		}
		shutdown.Register(server.Close)

		task.Init()
		go task.RUN()
	},
}

func init() {
	startCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is ./data/config.json)")
	rootCmd.AddCommand(startCmd)
}
