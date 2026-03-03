// Copyright 2026 NSP Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package config

import (
	"fmt"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

// viperLoader implements the Loader interface using spf13/viper as the underlying library.
type viperLoader struct {
	v         *viper.Viper          // viper instance
	mu        sync.RWMutex          // protects callbacks list and hot-reload concurrency
	callbacks []func(UnmarshalFunc) // registered change callbacks
	done      chan struct{}         // signal channel for Close
}

// newViperLoader creates a new viperLoader instance.
func newViperLoader(opt Option) (*viperLoader, error) {
	v := viper.New()

	// Configure file path
	if opt.ConfigFile != "" {
		v.SetConfigFile(opt.ConfigFile)
	} else {
		if opt.ConfigName == "" {
			return nil, fmt.Errorf("ConfigName must be specified when ConfigFile is empty")
		}
		if len(opt.ConfigPaths) == 0 {
			return nil, fmt.Errorf("ConfigPaths must be specified when ConfigFile is empty")
		}
		v.SetConfigName(opt.ConfigName)
		for _, path := range opt.ConfigPaths {
			v.AddConfigPath(path)
		}
	}

	// Set config type
	if opt.ConfigType != "" {
		v.SetConfigType(opt.ConfigType)
	}

	// Set default values
	for key, value := range opt.Defaults {
		v.SetDefault(key, value)
	}

	// Bind environment variables
	if opt.EnvPrefix != "" {
		v.SetEnvPrefix(opt.EnvPrefix)
		v.AutomaticEnv()
	}

	return &viperLoader{
		v:    v,
		done: make(chan struct{}),
	}, nil
}

// Load loads the configuration file and unmarshals it into target.
// Each call re-reads the file content.
func (l *viperLoader) Load(target any) error {
	if err := l.v.ReadInConfig(); err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	// Use mapstructure with standard settings
	// Note: Strict validation of unused fields is not directly supported by viper
	// Business code should validate configuration after loading if needed
	return l.v.Unmarshal(target)
}

// Unmarshal unmarshals the current in-memory configuration into target.
// Does not re-read the file, used in hot-reload callbacks.
func (l *viperLoader) Unmarshal(target any) error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.v.Unmarshal(target)
}

// OnChange registers a configuration change callback.
// If Watch=false, registering callbacks does not error but they will never be triggered.
func (l *viperLoader) OnChange(fn func(UnmarshalFunc)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.callbacks = append(l.callbacks, fn)
}

// Close stops file watching and releases resources.
// After Close, file change events will no longer trigger callbacks.
func (l *viperLoader) Close() {
	close(l.done)
	// Note: viper does not provide a method to stop WatchConfig.
	// We handle this by checking the done channel status in the OnConfigChange callback.
}

// startWatching starts file watching if Watch=true is configured.
// This should be called after all callbacks are registered.
func (l *viperLoader) startWatching() {
	l.v.WatchConfig()
	l.v.OnConfigChange(func(e fsnotify.Event) {
		// Check if loader is closed
		select {
		case <-l.done:
			return // Skip execution if closed
		default:
		}

		// Note: Kubernetes Secret volume updates use atomic symlink replacement,
		// which triggers fsnotify Create events. viper's internal fsnotify
		// already handles this scenario correctly, no additional adaptation needed.

		// Acquire write lock to protect callbacks list
		l.mu.Lock()
		
		// Create unmarshal function that holds read lock
		unmarshalFn := func(target any) error {
			l.mu.RLock()
			defer l.mu.RUnlock()
			return l.v.Unmarshal(target)
		}
		
		// Copy callbacks to avoid holding lock during execution
		callbacks := make([]func(UnmarshalFunc), len(l.callbacks))
		copy(callbacks, l.callbacks)
		l.mu.Unlock()

		// Execute callbacks in registration order
		// Individual callback panics are recovered to ensure subsequent callbacks execute
		for _, cb := range callbacks {
			func() {
				defer func() {
					if r := recover(); r != nil {
						// Log panic but continue executing remaining callbacks
						// Cannot use logger here to avoid dependency cycle
						fmt.Printf("config callback panic recovered: %v\n", r)
					}
				}()
				cb(unmarshalFn)
			}()
		}
	})
}