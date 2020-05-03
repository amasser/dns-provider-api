// Package netcup implements a DNS Provider for solving the DNS-01 challenge using the netcup DNS API.
package netcup

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/matthiasng/dns-provider-api/provider/netcup/internal"

	"github.com/go-acme/lego/v3/challenge/dns01"
	"github.com/go-acme/lego/v3/log"
	"github.com/go-acme/lego/v3/platform/config/env"
)

// Environment variables names.
const (
	envNamespace = "NETCUP_"

	EnvCustomerNumber = envNamespace + "CUSTOMER_NUMBER"
	EnvAPIKey         = envNamespace + "API_KEY"
	EnvAPIPassword    = envNamespace + "API_PASSWORD"

	EnvTTL                = envNamespace + "TTL"
	EnvPropagationTimeout = envNamespace + "PROPAGATION_TIMEOUT"
	EnvPollingInterval    = envNamespace + "POLLING_INTERVAL"
	EnvHTTPTimeout        = envNamespace + "HTTP_TIMEOUT"
)

// Config is used to configure the creation of the DNSProvider
type Config struct {
	Key                string
	Password           string
	Customer           string
	TTL                int
	PropagationTimeout time.Duration
	PollingInterval    time.Duration
	HTTPClient         *http.Client
}

// NewDefaultConfig returns a default configuration for the DNSProvider
func NewDefaultConfig() *Config {
	return &Config{
		TTL:                env.GetOrDefaultInt(EnvTTL, dns01.DefaultTTL),
		PropagationTimeout: env.GetOrDefaultSecond(EnvPropagationTimeout, 120*time.Second),
		PollingInterval:    env.GetOrDefaultSecond(EnvPollingInterval, 5*time.Second),
		HTTPClient: &http.Client{
			Timeout: env.GetOrDefaultSecond(EnvHTTPTimeout, 10*time.Second),
		},
	}
}

// DNSProvider is an implementation of the challenge.Provider interface
type DNSProvider struct {
	client *internal.Client
	config *Config
}

// NewDNSProvider returns a DNSProvider instance configured for netcup.
// Credentials must be passed in the environment variables:
// NETCUP_CUSTOMER_NUMBER, NETCUP_API_KEY, NETCUP_API_PASSWORD
func NewDNSProvider() (*DNSProvider, error) {
	values, err := env.Get(EnvCustomerNumber, EnvAPIKey, EnvAPIPassword)
	if err != nil {
		return nil, fmt.Errorf("netcup: %w", err)
	}

	config := NewDefaultConfig()
	config.Customer = values[EnvCustomerNumber]
	config.Key = values[EnvAPIKey]
	config.Password = values[EnvAPIPassword]

	return NewDNSProviderConfig(config)
}

// NewDNSProviderConfig return a DNSProvider instance configured for netcup.
func NewDNSProviderConfig(config *Config) (*DNSProvider, error) {
	if config == nil {
		return nil, errors.New("netcup: the configuration of the DNS provider is nil")
	}

	client, err := internal.NewClient(config.Customer, config.Key, config.Password)
	if err != nil {
		return nil, fmt.Errorf("netcup: %w", err)
	}

	client.HTTPClient = config.HTTPClient

	return &DNSProvider{client: client, config: config}, nil
}

// Present creates a TXT record to fulfill the dns-01 challenge
func (d *DNSProvider) Present(domainName, token, fqdn, value string) error {
	zone, err := dns01.FindZoneByFqdn(fqdn)
	if err != nil {
		return fmt.Errorf("netcup: failed to find DNSZone, %w", err)
	}

	sessionID, err := d.client.Login()
	if err != nil {
		return fmt.Errorf("netcup: %w", err)
	}

	defer func() {
		err = d.client.Logout(sessionID)
		if err != nil {
			log.Print("netcup: %v", err)
		}
	}()

	hostname := strings.Replace(fqdn, "."+zone, "", 1)
	record := internal.DNSRecord{
		Hostname:    hostname,
		RecordType:  "TXT",
		Destination: value,
		TTL:         d.config.TTL,
	}

	zone = dns01.UnFqdn(zone)

	records, err := d.client.GetDNSRecords(zone, sessionID)
	if err != nil {
		// skip no existing records
		log.Infof("no existing records, error ignored: %v", err)
	}

	records = append(records, record)

	err = d.client.UpdateDNSRecord(sessionID, zone, records)
	if err != nil {
		return fmt.Errorf("netcup: failed to add TXT-Record: %w", err)
	}

	return nil
}

// CleanUp removes the TXT record matching the specified parameters
func (d *DNSProvider) CleanUp(domainName, token, fqdn, value string) error {
	zone, err := dns01.FindZoneByFqdn(fqdn)
	if err != nil {
		return fmt.Errorf("netcup: failed to find DNSZone, %w", err)
	}

	sessionID, err := d.client.Login()
	if err != nil {
		return fmt.Errorf("netcup: %w", err)
	}

	defer func() {
		err = d.client.Logout(sessionID)
		if err != nil {
			log.Print("netcup: %v", err)
		}
	}()

	hostname := strings.Replace(fqdn, "."+zone, "", 1)

	zone = dns01.UnFqdn(zone)

	records, err := d.client.GetDNSRecords(zone, sessionID)
	if err != nil {
		return fmt.Errorf("netcup: %w", err)
	}

	record := internal.DNSRecord{
		Hostname:    hostname,
		RecordType:  "TXT",
		Destination: value,
	}

	idx, err := internal.GetDNSRecordIdx(records, record)
	if err != nil {
		return fmt.Errorf("netcup: %w", err)
	}

	records[idx].DeleteRecord = true

	err = d.client.UpdateDNSRecord(sessionID, zone, []internal.DNSRecord{records[idx]})
	if err != nil {
		return fmt.Errorf("netcup: %w", err)
	}

	return nil
}

// Timeout returns the timeout and interval to use when checking for DNS propagation.
// Adjusting here to cope with spikes in propagation times.
func (d *DNSProvider) Timeout() (timeout, interval time.Duration) {
	return d.config.PropagationTimeout, d.config.PollingInterval
}