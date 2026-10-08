// 本文件给出 dry-run 使用的公开标价。live 会在付款前再向 Porkbun 询价。
package procurement

import "strings"

// FixtureQuoteCents 是 2026-10-07 公开标价，单位是美分。
func FixtureQuoteCents(domain, kind string) (int64, bool) {
	tld := domain
	if i := strings.LastIndex(domain, "."); i >= 0 {
		tld = domain[i+1:]
	}
	var reg, renew int64
	switch strings.ToLower(tld) {
	case "dev":
		reg, renew = 875, 1287
	case "app":
		reg, renew = 875, 1493
	case "xyz":
		reg, renew = 204, 1421
	case "com":
		reg, renew = 1108, 1108
	default:
		return 0, false
	}
	if strings.Contains(kind, "renew") {
		return renew, true
	}
	return reg, true
}
