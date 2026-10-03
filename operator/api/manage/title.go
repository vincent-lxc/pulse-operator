// 本文件给只读 Manage 提供中英菜单标题。
package manage

// localeTitle 按框架语言码返回展示标题。未知语言回退中文。
func localeTitle(locale, zh, en string) string {
	if locale == "en-US" {
		return en
	}
	return zh
}
