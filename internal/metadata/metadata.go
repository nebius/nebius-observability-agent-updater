package metadata

import (
	"log/slog"
	"os"
	"strings"
)

type Config struct {
	Path               string `yaml:"path"`
	ParentIdFilename   string `yaml:"parent_id_filename"`
	InstanceIdFilename string `yaml:"instance_id_filename"`
	IamTokenFilename   string `yaml:"iam_token_filename"`
}

type Reader struct {
	cfg    Config
	logger *slog.Logger
}

func NewReader(cfg Config, logger *slog.Logger) *Reader {
	return &Reader{cfg: cfg, logger: logger}
}

func (r *Reader) GetParentId() (string, error) {
	return r.readAndTrimFile(r.cfg.Path + "/" + r.cfg.ParentIdFilename)
}

func (r *Reader) GetInstanceId() (string, error) {
	return r.readAndTrimFile(r.cfg.Path + "/" + r.cfg.InstanceIdFilename)
}

func (r *Reader) GetIamToken() (string, error) {
	return r.readAndTrimFile(r.cfg.Path + "/" + r.cfg.IamTokenFilename)
}

func (r *Reader) readAndTrimFile(filename string) (string, error) {
	r.logger.Debug("Reading file", "filename", filename)
	content, err := os.ReadFile(filename)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(content)), nil
}
