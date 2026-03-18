// examples/config/main.go
//
// 这个 demo 演示了 config 模块的核心功能：
//   1. 从 YAML 文件加载配置
//   2. 使用默认值补全缺失项
//   3. 使用环境变量覆盖配置（前缀 NSP）
//   4. 注册热更新回调，文件变化后自动重新加载
//
// 运行方式：
//
//	go run examples/config/main.go
//
// 验证热更新（另开一个终端执行）：
//
//	# 修改端口号，观察 demo 打印新配置
//	sed -i 's/port: 8080/port: 9090/' examples/config/config.yaml
//
// 验证环境变量覆盖：
//
//	NSP_SERVER_PORT=7777 go run examples/config/main.go
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jinleili-zz/nsp-platform/config"
)

// ServerConfig holds server-related configuration.
type ServerConfig struct {
	Host    string `mapstructure:"host"`
	Port    int    `mapstructure:"port"`
	Timeout int    `mapstructure:"timeout"`
}

// DatabaseConfig holds database-related configuration.
type DatabaseConfig struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Name     string `mapstructure:"name"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
	MaxConns int    `mapstructure:"max_conns"`
}

// LogConfig holds logging configuration.
type LogConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}

// AppConfig is the top-level application configuration.
type AppConfig struct {
	Server   ServerConfig   `mapstructure:"server"`
	Database DatabaseConfig `mapstructure:"database"`
	Log      LogConfig      `mapstructure:"log"`
	Debug    bool           `mapstructure:"debug"`
}

func printConfig(tag string, cfg AppConfig) {
	fmt.Printf("\n[%s] ========== Config Snapshot ==========\n", tag)
	fmt.Printf("  Server:\n")
	fmt.Printf("    Host:    %s\n", cfg.Server.Host)
	fmt.Printf("    Port:    %d\n", cfg.Server.Port)
	fmt.Printf("    Timeout: %ds\n", cfg.Server.Timeout)
	fmt.Printf("  Database:\n")
	fmt.Printf("    Host:     %s\n", cfg.Database.Host)
	fmt.Printf("    Port:     %d\n", cfg.Database.Port)
	fmt.Printf("    Name:     %s\n", cfg.Database.Name)
	fmt.Printf("    Username: %s\n", cfg.Database.Username)
	fmt.Printf("    MaxConns: %d\n", cfg.Database.MaxConns)
	fmt.Printf("  Log:\n")
	fmt.Printf("    Level:  %s\n", cfg.Log.Level)
	fmt.Printf("    Format: %s\n", cfg.Log.Format)
	fmt.Printf("  Debug: %v\n", cfg.Debug)
	fmt.Printf("======================================\n\n")
}

func main() {
	// Determine the config file path (relative to repo root or current dir)
	cfgFile := "examples/config/config.yaml"
	if _, err := os.Stat(cfgFile); os.IsNotExist(err) {
		cfgFile = "config.yaml"
	}

	fmt.Println("=== NSP Config Module Demo ===")
	fmt.Printf("Config file: %s\n", cfgFile)

	// Hint about env override
	if port := os.Getenv("NSP_SERVER_PORT"); port != "" {
		fmt.Printf("Detected env override: NSP_SERVER_PORT=%s\n", port)
	}

	// Initialize loader
	loader, err := config.New(config.Option{
		ConfigFile: cfgFile,
		Watch:      true,
		EnvPrefix:  "NSP",
		Defaults: map[string]any{
			"server.host":      "127.0.0.1",
			"server.port":      8080,
			"server.timeout":   30,
			"database.port":    5432,
			"database.max_conns": 5,
			"log.level":        "info",
			"log.format":       "json",
			"debug":            false,
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create config loader: %v\n", err)
		os.Exit(1)
	}
	defer loader.Close()

	// ── Step 1: Initial load ────────────────────────────────────────────────
	var cfg AppConfig
	if err := loader.Load(&cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}
	printConfig("INITIAL LOAD", cfg)

	// ── Step 2: Register hot-reload callback ────────────────────────────────
	reloadCount := 0
	loader.OnChange(func(apply func(target any) error) {
		var newCfg AppConfig
		if err := apply(&newCfg); err != nil {
			fmt.Fprintf(os.Stderr, "[HOT RELOAD] Failed to parse new config: %v\n", err)
			return
		}
		reloadCount++
		cfg = newCfg
		printConfig(fmt.Sprintf("HOT RELOAD #%d", reloadCount), cfg)
	})

	fmt.Println("Watching config file for changes. Modify examples/config/config.yaml to see hot reload.")
	fmt.Println("Press Ctrl+C to exit.")

	// ── Step 3: Trigger an automatic hot-reload demo after 2s ───────────────
	go func() {
		time.Sleep(2 * time.Second)

		fmt.Println("[DEMO] Auto-modifying config file to demonstrate hot reload...")
		content, err := os.ReadFile(cfgFile)
		if err != nil {
			return
		}

		// Save original
		original := string(content)

		// Write modified version
		modified := replacePort(original, 8080, 19090)
		if err := os.WriteFile(cfgFile, []byte(modified), 0644); err != nil {
			return
		}
		fmt.Println("[DEMO] Written port 19090 to config file.")

		// Restore original after 3s
		time.Sleep(3 * time.Second)
		fmt.Println("[DEMO] Restoring original config file...")
		if err := os.WriteFile(cfgFile, []byte(original), 0644); err != nil {
			return
		}
		fmt.Println("[DEMO] Restored original config file.")
	}()

	// Wait for signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	fmt.Printf("\nTotal hot reloads observed: %d\n", reloadCount)
	fmt.Println("Exiting.")
}

// replacePort replaces "port: <old>" with "port: <new>" in YAML content.
func replacePort(content string, old, new int) string {
	oldStr := fmt.Sprintf("port: %d", old)
	newStr := fmt.Sprintf("port: %d", new)
	result := []byte(content)
	// Simple byte-level replacement
	for i := 0; i+len(oldStr) <= len(result); i++ {
		if string(result[i:i+len(oldStr)]) == oldStr {
			result = append(result[:i], append([]byte(newStr), result[i+len(oldStr):]...)...)
			break
		}
	}
	return string(result)
}
