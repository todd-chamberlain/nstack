package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var version = "dev"

var rootCmd = &cobra.Command{
	Use:   "nstack",
	Short: "Deploy NVIDIA GPU infrastructure on Kubernetes",
	Long:  "NStack is a CLI tool for deploying and managing NVIDIA GPU infrastructure on Kubernetes clusters.",
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version of nstack",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("nstack %s\n", version)
	},
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().String("config", "", "config file (default: ~/.nstack/config.yaml)")
	rootCmd.PersistentFlags().String("site", "", "site name to operate on")
	rootCmd.PersistentFlags().StringP("output", "o", "text", "output format (text|json)")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "enable verbose output")
	rootCmd.PersistentFlags().BoolP("quiet", "q", false, "suppress non-essential output")
	rootCmd.PersistentFlags().BoolP("yes", "y", false, "auto-confirm prompts")

	_ = viper.BindPFlag("config", rootCmd.PersistentFlags().Lookup("config"))
	_ = viper.BindPFlag("site", rootCmd.PersistentFlags().Lookup("site"))
	_ = viper.BindPFlag("output", rootCmd.PersistentFlags().Lookup("output"))
	_ = viper.BindPFlag("verbose", rootCmd.PersistentFlags().Lookup("verbose"))
	_ = viper.BindPFlag("quiet", rootCmd.PersistentFlags().Lookup("quiet"))
	_ = viper.BindPFlag("yes", rootCmd.PersistentFlags().Lookup("yes"))

	rootCmd.AddCommand(versionCmd)
}

func initConfig() {
	cfgFile := viper.GetString("config")
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		viper.AddConfigPath(filepath.Join(home, ".nstack"))
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
	}

	// Read config file; silently ignore if not found.
	_ = viper.ReadInConfig()
}
