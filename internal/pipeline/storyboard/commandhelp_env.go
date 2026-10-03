package storyboard

import (
	_ "embed"
	"gopkg.in/yaml.v3"
	"net"
	"net/url"
	"sort"
	"strings"
)

//go:embed commandhelp-env.yaml
var commandHelpPolicyData []byte

type commandHelpPolicy struct {
	Common, Windows, Network []string
	ProxySchemes             []string `yaml:"proxy_schemes"`
	GoSchemes                []string `yaml:"go_schemes"`
}

func commandHelpEnvironment(entries []string, platform string) []string {
	var policy commandHelpPolicy
	if err := yaml.Unmarshal(commandHelpPolicyData, &policy); err != nil {
		panic(err)
	}
	allow := map[string]string{}
	for _, key := range append(policy.Common, policy.Network...) {
		allow[key] = key
	}
	if platform == "windows" {
		for _, key := range policy.Windows {
			allow[key] = key
		}
	}
	values := map[string]string{}
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if platform == "windows" {
			key = strings.ToUpper(key)
		}
		canonical, ok := allow[key]
		if !ok {
			continue
		}
		delete(values, canonical)
		if strings.ContainsAny(value, "\r\n") {
			continue
		}
		valid := true
		switch canonical {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY":
			valid = validHelpURL(value, policy.ProxySchemes, false)
		case "GOPROXY":
			for _, endpoint := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '|' }) {
				if endpoint != "direct" && endpoint != "off" && !validHelpURL(endpoint, policy.GoSchemes, true) {
					valid = false
				}
			}
			if value == "" || strings.HasPrefix(value, ",") || strings.HasPrefix(value, "|") || strings.HasSuffix(value, ",") || strings.HasSuffix(value, "|") || strings.Contains(value, ",,") || strings.Contains(value, "||") || strings.Contains(value, ",|") || strings.Contains(value, "|,") {
				valid = false
			}
		case "GOSUMDB":
			fields := strings.Fields(value)
			valid = len(fields) == 1 || len(fields) == 2
			if len(fields) == 2 {
				valid = validHelpURL(fields[1], policy.GoSchemes, false)
			}
		case "NO_PROXY":
			valid = validHelpNoProxy(value)
		}
		if valid {
			values[canonical] = value
		}
	}
	values["GOENV"] = "off"
	values["GOWORK"] = "off"
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return out
}
func validHelpURL(value string, schemes []string, allowFile bool) bool {
	u, err := url.Parse(value)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	accepted := false
	for _, scheme := range schemes {
		if u.Scheme == scheme {
			accepted = true
		}
	}
	if !accepted {
		return false
	}
	if u.Scheme == "file" {
		return allowFile && strings.HasPrefix(u.Path, "/") && (u.Host == "" || u.Host == "localhost")
	}
	return u.Hostname() != "" && !strings.ContainsAny(u.Hostname(), " \t\\")
}
func validHelpNoProxy(value string) bool {
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "*" {
			continue
		}
		if item == "" {
			return false
		}
		if net.ParseIP(item) != nil {
			continue
		}
		if _, _, err := net.ParseCIDR(item); err == nil {
			continue
		}
		host := strings.TrimPrefix(item, ".")
		if strings.HasPrefix(host, "*.") {
			host = strings.TrimPrefix(host, "*.")
		}
		if host == "" || strings.ContainsAny(host, ":/\\@?# \t*") {
			return false
		}
		for _, char := range host {
			if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '.' || char == '-') {
				return false
			}
		}
	}
	return true
}
