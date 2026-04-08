package wt

import (
	"fmt"
	"os"
	"time"

	"github.com/wwweww/go-wst/internal/protocol"
)

// WtConf is the configuration for WebTransport server.
type WtConf struct {
	Name string `json:",optional"`

	// Network configuration
	Addr string `json:",default=:443"` // Listen address (WebTransport requires HTTPS)
	Path string `json:",default=/wt"`  // WebTransport path

	// TLS configuration (required for WebTransport)
	// Load certificate from file:
	CertFile string `json:",optional"` // TLS certificate file path
	KeyFile  string `json:",optional"` // TLS private key file path
	// Load certificate from environment variables (takes priority over file paths):
	CertEnvVar string `json:",optional"` // Env var name containing TLS certificate PEM content
	KeyEnvVar  string `json:",optional"` // Env var name containing TLS private key PEM content

	// Connection limits
	MaxConnections    int64 `json:",default=10000"` // Maximum concurrent connections
	MaxStreamsPerConn int64 `json:",default=100"`   // Maximum streams per connection

	// Buffer configuration
	ReadBufferSize  int `json:",default=4096"` // Read buffer size
	WriteBufferSize int `json:",default=4096"` // Write buffer size

	// Timeout configuration
	IdleTimeout       time.Duration `json:",default=120s"` // Idle timeout
	KeepAliveInterval time.Duration `json:",default=30s"`  // Keep-alive interval
	WriteTimeout      time.Duration `json:",default=10s"`  // Write timeout

	// Authentication configuration
	Auth protocol.AuthConfig `json:",optional"`

	// Resolved paths after SetUp (unexported, used internally)
	resolvedCertFile string
	resolvedKeyFile  string
	tempFiles        []string // temp files to clean up on stop
}

// SetUp initializes the configuration and resolves TLS certificate sources.
// Priority: CertEnvVar/KeyEnvVar > CertFile/KeyFile
func (c *WtConf) SetUp() error {
	if err := c.resolveCertFile(); err != nil {
		return err
	}
	return c.resolveKeyFile()
}

func (c *WtConf) resolveCertFile() error {
	// Env var takes priority
	if c.CertEnvVar != "" {
		content := os.Getenv(c.CertEnvVar)
		if content == "" {
			return fmt.Errorf("environment variable %s is not set or empty", c.CertEnvVar)
		}
		tmpFile, err := c.writeTempFile("wt-cert-*.pem", []byte(content))
		if err != nil {
			return fmt.Errorf("failed to write cert from env var %s: %w", c.CertEnvVar, err)
		}
		c.resolvedCertFile = tmpFile
		return nil
	}

	if c.CertFile != "" {
		c.resolvedCertFile = c.CertFile
		return nil
	}

	return fmt.Errorf("TLS certificate not configured: set CertFile or CertEnvVar")
}

func (c *WtConf) resolveKeyFile() error {
	if c.KeyEnvVar != "" {
		content := os.Getenv(c.KeyEnvVar)
		if content == "" {
			return fmt.Errorf("environment variable %s is not set or empty", c.KeyEnvVar)
		}
		tmpFile, err := c.writeTempFile("wt-key-*.pem", []byte(content))
		if err != nil {
			return fmt.Errorf("failed to write key from env var %s: %w", c.KeyEnvVar, err)
		}
		c.resolvedKeyFile = tmpFile
		return nil
	}

	if c.KeyFile != "" {
		c.resolvedKeyFile = c.KeyFile
		return nil
	}

	return fmt.Errorf("TLS private key not configured: set KeyFile or KeyEnvVar")
}

func (c *WtConf) writeTempFile(pattern string, data []byte) (string, error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}

	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}

	// Restrict permissions (best-effort on Windows)
	_ = os.Chmod(f.Name(), 0600)

	c.tempFiles = append(c.tempFiles, f.Name())
	return f.Name(), nil
}

// Cleanup removes temp files created from environment variable content.
func (c *WtConf) Cleanup() {
	for _, f := range c.tempFiles {
		_ = os.Remove(f)
	}
	c.tempFiles = nil
}

// StatelessConf is the configuration for stateless mode.
type StatelessConf struct {
	// Polling configuration
	PollInterval time.Duration `json:",default=100ms"` // Polling interval
	BatchSize    int           `json:",default=100"`   // Messages per batch

	// Acknowledgment
	AckTimeout time.Duration `json:",default=30s"` // Ack timeout
}
