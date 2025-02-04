package metadata

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

type Config struct {
	Path                  string `yaml:"path"`
	ParentIdFilename      string `yaml:"parent_id_filename"`
	InstanceIdFilename    string `yaml:"instance_id_filename"`
	IamTokenFilename      string `yaml:"iam_token_filename"`
	Mk8sClusterIdFilename string `yaml:"mk8s_cluster_id_filename"`
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

func (r *Reader) GetInstanceId() (instanceId string, isFallback bool, err error) {
	cmd := exec.Command("cloud-init", "query", "instance-id")
	output, err := cmd.Output()
	if err != nil {
		instanceId, err2 := r.readAndTrimFile(r.cfg.Path + "/" + r.cfg.InstanceIdFilename)
		if err2 != nil {
			return "", true, fmt.Errorf("failed to call cloud-init query instance-id: %w and read from file: %w", err, err2)
		}
		return instanceId, true, nil
	}
	return strings.TrimSpace(string(output)), false, nil
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
