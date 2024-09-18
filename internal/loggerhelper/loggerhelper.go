package loggerhelper

import (
	"log/slog"
	"os"
)

type LogConfig struct {
	LogLevel string `yaml:"log_level"`
}

func getLogLevel(levelStr string) slog.Level {
	switch levelStr {
	case "DEBUG":
		return slog.LevelDebug
	case "INFO":
		return slog.LevelInfo
	case "WARN":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func InitLogger(cfg *LogConfig) *slog.Logger {
	logLevel := getLogLevel(cfg.LogLevel)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))

	return logger
}
