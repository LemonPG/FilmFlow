package main

import (
	"log"
	"os"

	"github.com/LemonPG/FilmFlow/internal/app"

	"github.com/spf13/cobra"
)

func main() {
	var cfgPath string

	rootCmd := &cobra.Command{
		Use:   "FilmFlow",
		Short: "115网盘 strm 文件生成器",
		Long:  "FilmFlow 是一个用于从115网盘生成 strm 文件的工具",
		Run: func(cmd *cobra.Command, args []string) {
			if err := app.Run(cfgPath); err != nil {
				log.Fatal(err)
			}
		},
	}

	rootCmd.Flags().StringVarP(&cfgPath, "config", "c", "config.json", "配置文件路径")

	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
		os.Exit(1)
	}
}
