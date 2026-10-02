package resources

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// httpOrigin canonicalizes the scheme, hostname and effective port. Paths do
// not participate in origin comparison; subdomains are separate origins.
func httpOrigin(u *url.URL) (string, error) {
	scheme := strings.ToLower(u.Scheme)
	if (scheme != "http" && scheme != "https") || u.Hostname() == "" {
		return "", fmt.Errorf("an absolute HTTP or HTTPS URL is required")
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if scheme == "https" {
			port = "443"
		}
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", fmt.Errorf("port must be between 1 and 65535")
	}
	return scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), strconv.Itoa(portNumber)), nil
}

func pypiAuthOriginAllowed(target *url.URL, m PyPIDownloadModel) bool {
	origin, err := httpOrigin(target)
	if err != nil {
		return false
	}
	index, err := url.Parse(strings.TrimSpace(m.IndexURL.ValueString()))
	if err == nil {
		if indexOrigin, err := httpOrigin(index); err == nil && origin == indexOrigin {
			return true
		}
	}
	for _, element := range m.TrustedAuthOrigins.Elements() {
		value, ok := element.(types.String)
		if !ok || value.IsNull() || value.IsUnknown() {
			continue
		}
		trusted, err := parseTrustedAuthOrigin(value.ValueString())
		if err == nil && trusted == origin {
			return true
		}
	}
	return false
}

func parseTrustedAuthOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid origin URL")
	}
	if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", fmt.Errorf("origins must not contain credentials, a path, a query, or a fragment")
	}
	if strings.Contains(u.Host, "*") {
		return "", fmt.Errorf("wildcard hosts are not allowed")
	}
	return httpOrigin(u)
}

type trustedAuthOriginsValidator struct{}

func (trustedAuthOriginsValidator) Description(context.Context) string {
	return "each trusted origin must contain an HTTP(S) scheme, an exact host, and an optional port"
}

func (v trustedAuthOriginsValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (trustedAuthOriginsValidator) ValidateSet(_ context.Context, req validator.SetRequest, resp *validator.SetResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	for _, element := range req.ConfigValue.Elements() {
		value := element.(types.String)
		if value.IsUnknown() {
			continue
		}
		if _, err := parseTrustedAuthOrigin(value.ValueString()); err != nil {
			resp.Diagnostics.AddAttributeError(req.Path.AtSetValue(value), "Invalid trusted_auth_origins", err.Error())
		}
	}
}
