package listenerutil

import (
	"errors"
	"fmt"
	"net/textproto"
	"strings"
	"time"

	"github.com/hashicorp/go-multierror"
	"github.com/hashicorp/go-secure-stdlib/parseutil"
	"github.com/hashicorp/go-secure-stdlib/strutil"
	"github.com/hashicorp/go-secure-stdlib/tlsutil"
	"github.com/hashicorp/go-sockaddr"
	"github.com/hashicorp/hcl"
	"github.com/hashicorp/hcl/hcl/ast"
)

type ListenerTelemetry struct {
	UnauthenticatedMetricsAccess    bool        `hcl:"-"`
	UnauthenticatedMetricsAccessRaw interface{} `hcl:"unauthenticated_metrics_access"`
}

// ListenerConfig is the listener configuration for the server.
type ListenerConfig struct {
	RawConfig map[string]interface{}

	Type       string
	Purpose    []string    `hcl:"-"`
	PurposeRaw interface{} `hcl:"purpose"`

	Address                 string        `hcl:"address"`
	ClusterAddress          string        `hcl:"cluster_address"`
	MaxRequestSize          int64         `hcl:"-"`
	MaxRequestSizeRaw       interface{}   `hcl:"max_request_size"`
	MaxRequestDuration      time.Duration `hcl:"-"`
	MaxRequestDurationRaw   interface{}   `hcl:"max_request_duration"`
	RequireRequestHeader    bool          `hcl:"-"`
	RequireRequestHeaderRaw interface{}   `hcl:"require_request_header"`

	TLSDisable                       bool        `hcl:"-"`
	TLSDisableRaw                    interface{} `hcl:"tls_disable"`
	TLSCertFile                      string      `hcl:"tls_cert_file"`
	TLSKeyFile                       string      `hcl:"tls_key_file"`
	TLSMinVersion                    string      `hcl:"tls_min_version"`
	TLSMaxVersion                    string      `hcl:"tls_max_version"`
	TLSCipherSuites                  []uint16    `hcl:"-"`
	TLSCipherSuitesRaw               string      `hcl:"tls_cipher_suites"`
	TLSPreferServerCipherSuites      bool        `hcl:"-"`
	TLSPreferServerCipherSuitesRaw   interface{} `hcl:"tls_prefer_server_cipher_suites"`
	TLSRequireAndVerifyClientCert    bool        `hcl:"-"`
	TLSRequireAndVerifyClientCertRaw interface{} `hcl:"tls_require_and_verify_client_cert"`
	TLSClientCAFile                  string      `hcl:"tls_client_ca_file"`
	TLSDisableClientCerts            bool        `hcl:"-"`
	TLSDisableClientCertsRaw         interface{} `hcl:"tls_disable_client_certs"`

	HTTPReadTimeout          time.Duration `hcl:"-"`
	HTTPReadTimeoutRaw       interface{}   `hcl:"http_read_timeout"`
	HTTPReadHeaderTimeout    time.Duration `hcl:"-"`
	HTTPReadHeaderTimeoutRaw interface{}   `hcl:"http_read_header_timeout"`
	HTTPWriteTimeout         time.Duration `hcl:"-"`
	HTTPWriteTimeoutRaw      interface{}   `hcl:"http_write_timeout"`
	HTTPIdleTimeout          time.Duration `hcl:"-"`
	HTTPIdleTimeoutRaw       interface{}   `hcl:"http_idle_timeout"`

	ProxyProtocolBehavior           string                        `hcl:"proxy_protocol_behavior"`
	ProxyProtocolAuthorizedAddrs    []*sockaddr.SockAddrMarshaler `hcl:"-"`
	ProxyProtocolAuthorizedAddrsRaw interface{}                   `hcl:"proxy_protocol_authorized_addrs"`

	XForwardedForAuthorizedAddrs        []*sockaddr.SockAddrMarshaler `hcl:"-"`
	XForwardedForAuthorizedAddrsRaw     interface{}                   `hcl:"x_forwarded_for_authorized_addrs"`
	XForwardedForHopSkips               int64                         `hcl:"-"`
	XForwardedForHopSkipsRaw            interface{}                   `hcl:"x_forwarded_for_hop_skips"`
	XForwardedForRejectNotPresent       bool                          `hcl:"-"`
	XForwardedForRejectNotPresentRaw    interface{}                   `hcl:"x_forwarded_for_reject_not_present"`
	XForwardedForRejectNotAuthorized    bool                          `hcl:"-"`
	XForwardedForRejectNotAuthorizedRaw interface{}                   `hcl:"x_forwarded_for_reject_not_authorized"`

	SocketMode  string `hcl:"socket_mode"`
	SocketUser  string `hcl:"socket_user"`
	SocketGroup string `hcl:"socket_group"`

	Telemetry ListenerTelemetry `hcl:"telemetry"`

	// RandomPort is used only for some testing purposes
	RandomPort bool `hcl:"-"`

	CorsEnabledRaw                           interface{} `hcl:"cors_enabled"`
	CorsEnabled                              *bool       `hcl:"-"`
	CorsDisableDefaultAllowedOriginValuesRaw interface{} `hcl:"cors_disable_default_allowed_origin_values"`
	CorsDisableDefaultAllowedOriginValues    *bool       `hcl:"-"`
	CorsAllowedOrigins                       []string    `hcl:"cors_allowed_origins"`
	CorsAllowedHeaders                       []string    `hcl:"-"`
	CorsAllowedHeadersRaw                    []string    `hcl:"cors_allowed_headers"`
}

func (l *ListenerConfig) GoString() string {
	return fmt.Sprintf("*%#v", *l)
}

func ParseListeners(list *ast.ObjectList) ([]*ListenerConfig, error) {
	var err error
	result := make([]*ListenerConfig, 0, len(list.Items))
	for i, item := range list.Items {
		var l ListenerConfig
		if err := hcl.DecodeObject(&l, item.Val); err != nil {
			return nil, multierror.Prefix(err, fmt.Sprintf("listeners.%d:", i))
		}

		// Hacky way, for now, to get the values we want for sanitizing
		var m map[string]interface{}
		if err := hcl.DecodeObject(&m, item.Val); err != nil {
			return nil, multierror.Prefix(err, fmt.Sprintf("listeners.%d:", i))
		}
		l.RawConfig = m

		// Base values
		{
			switch {
			case l.Type != "":
			case len(item.Keys) == 1:
				l.Type = strings.ToLower(item.Keys[0].Token.Value().(string))
			default:
				return nil, multierror.Prefix(errors.New("listener type must be specified"), fmt.Sprintf("listeners.%d:", i))
			}

			l.Type = strings.ToLower(l.Type)
			switch l.Type {
			case "tcp", "unix":
			default:
				return nil, multierror.Prefix(fmt.Errorf("unsupported listener type %q", l.Type), fmt.Sprintf("listeners.%d:", i))
			}

			if l.PurposeRaw != nil {
				if l.Purpose, err = parseutil.ParseCommaStringSlice(l.PurposeRaw); err != nil {
					return nil, multierror.Prefix(fmt.Errorf("unable to parse 'purpose' in listener type %q: %w", l.Type, err), fmt.Sprintf("listeners.%d:", i))
				}
				for i, v := range l.Purpose {
					l.Purpose[i] = strings.ToLower(v)
				}

				l.PurposeRaw = nil
			}
		}

		// Request Parameters
		{
			if l.MaxRequestSizeRaw != nil {
				if l.MaxRequestSize, err = parseutil.ParseInt(l.MaxRequestSizeRaw); err != nil {
					return nil, multierror.Prefix(fmt.Errorf("error parsing max_request_size: %w", err), fmt.Sprintf("listeners.%d", i))
				}

				if l.MaxRequestSize < 0 {
					return nil, multierror.Prefix(errors.New("max_request_size cannot be negative"), fmt.Sprintf("listeners.%d", i))
				}

				l.MaxRequestSizeRaw = nil
			}

			if l.MaxRequestDurationRaw != nil {
				if l.MaxRequestDuration, err = parseutil.ParseDurationSecond(l.MaxRequestDurationRaw); err != nil {
					return nil, multierror.Prefix(fmt.Errorf("error parsing max_request_duration: %w", err), fmt.Sprintf("listeners.%d", i))
				}
				if l.MaxRequestDuration < 0 {
					return nil, multierror.Prefix(errors.New("max_request_duration cannot be negative"), fmt.Sprintf("listeners.%d", i))
				}

				l.MaxRequestDurationRaw = nil
			}

			if l.RequireRequestHeaderRaw != nil {
				if l.RequireRequestHeader, err = parseutil.ParseBool(l.RequireRequestHeaderRaw); err != nil {
					return nil, multierror.Prefix(fmt.Errorf("invalid value for require_request_header: %w", err), fmt.Sprintf("listeners.%d", i))
				}

				l.RequireRequestHeaderRaw = nil
			}
		}

		// TLS Parameters
		{
			if l.TLSDisableRaw != nil {
				if l.TLSDisable, err = parseutil.ParseBool(l.TLSDisableRaw); err != nil {
					return nil, multierror.Prefix(fmt.Errorf("invalid value for tls_disable: %w", err), fmt.Sprintf("listeners.%d", i))
				}

				l.TLSDisableRaw = nil
			}

			if l.TLSCipherSuitesRaw != "" {
				if l.TLSCipherSuites, err = tlsutil.ParseCiphers(l.TLSCipherSuitesRaw); err != nil {
					return nil, multierror.Prefix(fmt.Errorf("invalid value for tls_cipher_suites: %w", err), fmt.Sprintf("listeners.%d", i))
				}
			}

			if l.TLSPreferServerCipherSuitesRaw != nil {
				if l.TLSPreferServerCipherSuites, err = parseutil.ParseBool(l.TLSPreferServerCipherSuitesRaw); err != nil {
					return nil, multierror.Prefix(fmt.Errorf("invalid value for tls_prefer_server_cipher_suites: %w", err), fmt.Sprintf("listeners.%d", i))
				}

				l.TLSPreferServerCipherSuitesRaw = nil
			}

			if l.TLSRequireAndVerifyClientCertRaw != nil {
				if l.TLSRequireAndVerifyClientCert, err = parseutil.ParseBool(l.TLSRequireAndVerifyClientCertRaw); err != nil {
					return nil, multierror.Prefix(fmt.Errorf("invalid value for tls_require_and_verify_client_cert: %w", err), fmt.Sprintf("listeners.%d", i))
				}

				l.TLSRequireAndVerifyClientCertRaw = nil
			}

			if l.TLSDisableClientCertsRaw != nil {
				if l.TLSDisableClientCerts, err = parseutil.ParseBool(l.TLSDisableClientCertsRaw); err != nil {
					return nil, multierror.Prefix(fmt.Errorf("invalid value for tls_disable_client_certs: %w", err), fmt.Sprintf("listeners.%d", i))
				}

				l.TLSDisableClientCertsRaw = nil
			}
		}

		// HTTP timeouts
		{
			if l.HTTPReadTimeoutRaw != nil {
				if l.HTTPReadTimeout, err = parseutil.ParseDurationSecond(l.HTTPReadTimeoutRaw); err != nil {
					return nil, multierror.Prefix(fmt.Errorf("error parsing http_read_timeout: %w", err), fmt.Sprintf("listeners.%d", i))
				}

				l.HTTPReadTimeoutRaw = nil
			}

			if l.HTTPReadHeaderTimeoutRaw != nil {
				if l.HTTPReadHeaderTimeout, err = parseutil.ParseDurationSecond(l.HTTPReadHeaderTimeoutRaw); err != nil {
					return nil, multierror.Prefix(fmt.Errorf("error parsing http_read_header_timeout: %w", err), fmt.Sprintf("listeners.%d", i))
				}

				l.HTTPReadHeaderTimeoutRaw = nil
			}

			if l.HTTPWriteTimeoutRaw != nil {
				if l.HTTPWriteTimeout, err = parseutil.ParseDurationSecond(l.HTTPWriteTimeoutRaw); err != nil {
					return nil, multierror.Prefix(fmt.Errorf("error parsing http_write_timeout: %w", err), fmt.Sprintf("listeners.%d", i))
				}

				l.HTTPWriteTimeoutRaw = nil
			}

			if l.HTTPIdleTimeoutRaw != nil {
				if l.HTTPIdleTimeout, err = parseutil.ParseDurationSecond(l.HTTPIdleTimeoutRaw); err != nil {
					return nil, multierror.Prefix(fmt.Errorf("error parsing http_idle_timeout: %w", err), fmt.Sprintf("listeners.%d", i))
				}

				l.HTTPIdleTimeoutRaw = nil
			}
		}

		// Proxy Protocol config
		{
			if l.ProxyProtocolAuthorizedAddrsRaw != nil {
				if l.ProxyProtocolAuthorizedAddrs, err = parseutil.ParseAddrs(l.ProxyProtocolAuthorizedAddrsRaw); err != nil {
					return nil, multierror.Prefix(fmt.Errorf("error parsing proxy_protocol_authorized_addrs: %w", err), fmt.Sprintf("listeners.%d", i))
				}

				switch l.ProxyProtocolBehavior {
				case "allow_authorized", "deny_authorized":
					if len(l.ProxyProtocolAuthorizedAddrs) == 0 {
						return nil, multierror.Prefix(errors.New("proxy_protocol_behavior set to allow or deny only authorized addresses but no proxy_protocol_authorized_addrs value"), fmt.Sprintf("listeners.%d", i))
					}
				}

				l.ProxyProtocolAuthorizedAddrsRaw = nil
			}
		}

		// X-Forwarded-For config
		{
			if l.XForwardedForAuthorizedAddrsRaw != nil {
				if l.XForwardedForAuthorizedAddrs, err = parseutil.ParseAddrs(l.XForwardedForAuthorizedAddrsRaw); err != nil {
					return nil, multierror.Prefix(fmt.Errorf("error parsing x_forwarded_for_authorized_addrs: %w", err), fmt.Sprintf("listeners.%d", i))
				}

				l.XForwardedForAuthorizedAddrsRaw = nil
			}

			if l.XForwardedForHopSkipsRaw != nil {
				if l.XForwardedForHopSkips, err = parseutil.ParseInt(l.XForwardedForHopSkipsRaw); err != nil {
					return nil, multierror.Prefix(fmt.Errorf("error parsing x_forwarded_for_hop_skips: %w", err), fmt.Sprintf("listeners.%d", i))
				}

				if l.XForwardedForHopSkips < 0 {
					return nil, multierror.Prefix(fmt.Errorf("x_forwarded_for_hop_skips cannot be negative but set to %d", l.XForwardedForHopSkips), fmt.Sprintf("listeners.%d", i))
				}

				l.XForwardedForHopSkipsRaw = nil
			}

			if l.XForwardedForRejectNotAuthorizedRaw != nil {
				if l.XForwardedForRejectNotAuthorized, err = parseutil.ParseBool(l.XForwardedForRejectNotAuthorizedRaw); err != nil {
					return nil, multierror.Prefix(fmt.Errorf("invalid value for x_forwarded_for_reject_not_authorized: %w", err), fmt.Sprintf("listeners.%d", i))
				}

				l.XForwardedForRejectNotAuthorizedRaw = nil
			}

			if l.XForwardedForRejectNotPresentRaw != nil {
				if l.XForwardedForRejectNotPresent, err = parseutil.ParseBool(l.XForwardedForRejectNotPresentRaw); err != nil {
					return nil, multierror.Prefix(fmt.Errorf("invalid value for x_forwarded_for_reject_not_present: %w", err), fmt.Sprintf("listeners.%d", i))
				}

				l.XForwardedForRejectNotPresentRaw = nil
			}
		}

		// Telemetry
		{
			if l.Telemetry.UnauthenticatedMetricsAccessRaw != nil {
				if l.Telemetry.UnauthenticatedMetricsAccess, err = parseutil.ParseBool(l.Telemetry.UnauthenticatedMetricsAccessRaw); err != nil {
					return nil, multierror.Prefix(fmt.Errorf("invalid value for telemetry.unauthenticated_metrics_access: %w", err), fmt.Sprintf("listeners.%d", i))
				}

				l.Telemetry.UnauthenticatedMetricsAccessRaw = nil
			}
		}

		// CORS
		{
			if l.CorsEnabledRaw != nil {
				corsEnabled, err := parseutil.ParseBool(l.CorsEnabledRaw)
				if err != nil {
					return nil, multierror.Prefix(fmt.Errorf("invalid value for cors_enabled: %w", err), fmt.Sprintf("listeners.%d", i))
				}
				l.CorsEnabled = &corsEnabled
				l.CorsEnabledRaw = nil
			}

			if l.CorsDisableDefaultAllowedOriginValuesRaw != nil {
				disabled, err := parseutil.ParseBool(l.CorsDisableDefaultAllowedOriginValuesRaw)
				if err != nil {
					return nil, multierror.Prefix(fmt.Errorf("invalid value for cors_disable_default_allowed_origin_values: %w", err), fmt.Sprintf("listeners.%d", i))
				}
				l.CorsDisableDefaultAllowedOriginValues = &disabled
				l.CorsDisableDefaultAllowedOriginValuesRaw = nil
			}

			if strutil.StrListContains(l.CorsAllowedOrigins, "*") && len(l.CorsAllowedOrigins) > 1 {
				return nil, multierror.Prefix(errors.New("cors_allowed_origins must only contain a wildcard or only non-wildcard values"), fmt.Sprintf("listeners.%d", i))
			}

			if len(l.CorsAllowedHeadersRaw) > 0 {
				for _, header := range l.CorsAllowedHeadersRaw {
					l.CorsAllowedHeaders = append(l.CorsAllowedHeaders, textproto.CanonicalMIMEHeaderKey(header))
				}
			}
		}

		result = append(result, &l)
	}

	return result, nil
}
