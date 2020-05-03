package checkdomain

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/go-acme/lego/v3/platform/config/env"
)

// Environment variables names.
const (
	envNamespace = "CHECKDOMAIN_"

	EnvEndpoint = envNamespace + "ENDPOINT"
	EnvToken    = envNamespace + "TOKEN"

	EnvTTL                = envNamespace + "TTL"
	EnvPropagationTimeout = envNamespace + "PROPAGATION_TIMEOUT"
	EnvPollingInterval    = envNamespace + "POLLING_INTERVAL"
	EnvHTTPTimeout        = envNamespace + "HTTP_TIMEOUT"
)

const (
	defaultEndpoint = "https://api.checkdomain.de"
	defaultTTL      = 300
)

// Config is used to configure the creation of the DNSProvider
type Config struct {
	Endpoint           *url.URL
	Token              string
	TTL                int
	PropagationTimeout time.Duration
	PollingInterval    time.Duration
	HTTPClient         *http.Client
}

// NewDefaultConfig returns a default configuration for the DNSProvider
func NewDefaultConfig() *Config {
	return &Config{
		TTL:                env.GetOrDefaultInt(EnvTTL, defaultTTL),
		PropagationTimeout: env.GetOrDefaultSecond(EnvPropagationTimeout, 5*time.Minute),
		PollingInterval:    env.GetOrDefaultSecond(EnvPollingInterval, 7*time.Second),
		HTTPClient: &http.Client{
			Timeout: env.GetOrDefaultSecond(EnvHTTPTimeout, 30*time.Second),
		},
	}
}

// DNSProvider implements challenge.Provider for the checkdomain API
// specified at https://developer.checkdomain.de/reference/.
type DNSProvider struct {
	config *Config

	domainIDMu      sync.Mutex
	domainIDMapping map[string]int
}

func NewDNSProvider() (*DNSProvider, error) {
	values, err := env.Get(EnvToken)
	if err != nil {
		return nil, fmt.Errorf("checkdomain: %w", err)
	}

	config := NewDefaultConfig()
	config.Token = values[EnvToken]

	endpoint, err := url.Parse(env.GetOrDefaultString(EnvEndpoint, defaultEndpoint))
	if err != nil {
		return nil, fmt.Errorf("checkdomain: invalid %s: %w", EnvEndpoint, err)
	}
	config.Endpoint = endpoint

	return NewDNSProviderConfig(config)
}

func NewDNSProviderConfig(config *Config) (*DNSProvider, error) {
	if config.Endpoint == nil {
		return nil, errors.New("checkdomain: invalid endpoint")
	}

	if config.Token == "" {
		return nil, errors.New("checkdomain: missing token")
	}

	if config.HTTPClient == nil {
		config.HTTPClient = http.DefaultClient
	}

	return &DNSProvider{
		config:          config,
		domainIDMapping: make(map[string]int),
	}, nil
}

// Present creates a TXT record to fulfill the dns-01 challenge
func (d *DNSProvider) Present(domain, token, fqdn, value string) error {
	domainID, err := d.getDomainIDByName(domain)
	if err != nil {
		return fmt.Errorf("checkdomain: %w", err)
	}

	err = d.checkNameservers(domainID)
	if err != nil {
		return fmt.Errorf("checkdomain: %w", err)
	}

	err = d.createRecord(domainID, &Record{
		Name:  fqdn,
		TTL:   d.config.TTL,
		Type:  "TXT",
		Value: value,
	})

	if err != nil {
		return fmt.Errorf("checkdomain: %w", err)
	}

	return nil
}

// CleanUp removes the TXT record previously created
func (d *DNSProvider) CleanUp(domain, token, fqdn, value string) error {
	domainID, err := d.getDomainIDByName(domain)
	if err != nil {
		return fmt.Errorf("checkdomain: %w", err)
	}

	err = d.checkNameservers(domainID)
	if err != nil {
		return fmt.Errorf("checkdomain: %w", err)
	}

	err = d.deleteTXTRecord(domainID, fqdn, value)
	if err != nil {
		return fmt.Errorf("checkdomain: %w", err)
	}

	d.domainIDMu.Lock()
	delete(d.domainIDMapping, fqdn)
	d.domainIDMu.Unlock()

	return nil
}

func (d *DNSProvider) Timeout() (timeout, interval time.Duration) {
	return d.config.PropagationTimeout, d.config.PollingInterval
}
