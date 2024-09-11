package metadata

import (
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
	cfg Config
}

func NewReader(cfg Config) *Reader {
	return &Reader{cfg: cfg}
}

func (r *Reader) GetParentId() (string, error) {
	return readAndTrimFile(r.cfg.Path + "/" + r.cfg.ParentIdFilename)
}

func (r *Reader) GetInstanceId() (string, error) {
	return readAndTrimFile(r.cfg.Path + "/" + r.cfg.InstanceIdFilename)
}

func (r *Reader) GetIamToken() (string, error) {
	return readAndTrimFile(r.cfg.Path + "/" + r.cfg.IamTokenFilename)
}

func readAndTrimFile(filename string) (string, error) {
	content, err := os.ReadFile(filename)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(content)), nil
}
