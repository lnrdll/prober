package output

import (
	"encoding/json"
	"os"
	"time"
)

type FilePublisher struct {
	file *os.File
}

func init() {
	Register("file", ExtensionHook{
		SetupFlags: func(ctx RuntimeContext, registerFlag func(func())) {
			path := new(string)
			ctx.Set("log_file_path", path)
			registerFlag(func() {
				BindStringFlag("log-file", "", "Append structured JSON log lines to a file", path)
			})
		},
		Factory: func(ctx RuntimeContext) (Publisher, error) {
			path := ctx.GetString("log_file_path")
			if path == "" {
				return nil, nil
			}
			f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				return nil, err
			}
			return &FilePublisher{file: f}, nil
		},
	})
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

func (p *FilePublisher) Publish(res Result) error {
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

func (p *FilePublisher) Close() error { return p.file.Close() }
