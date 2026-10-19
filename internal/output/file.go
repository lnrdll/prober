package output

import (
	"encoding/json"
	"os"
	"time"
)

type filePublisher struct {
	file *os.File
}

func newFilePublisher(cfg Config) (publisher, error) {
	f, err := os.OpenFile(cfg.File.Value, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	return &filePublisher{file: f}, nil
}

type fileLogSchema struct {
	Timestamp string            `json:"time"`
	URL       string            `json:"url"`
	Up        bool              `json:"up"`
	Status    int               `json:"status"`
	LatencyMS int64             `json:"latency_ms"`
	Tags      map[string]string `json:"tags,omitempty"`
	Error     string            `json:"error,omitempty"`
}

func (p *filePublisher) Publish(res Result) error {
	bytes, err := json.Marshal(fileLogSchema{
		Timestamp: res.Timestamp.Format(time.RFC3339),
		URL:       res.URL,
		Up:        res.Up,
		Status:    res.Status,
		LatencyMS: res.LatencyMS,
		Tags:      res.Tags,
		Error:     res.Error,
	})
	if err != nil {
		return err
	}
	_, err = p.file.Write(append(bytes, '\n'))
	return err
}

func (p *filePublisher) Close() error { return p.file.Close() }
