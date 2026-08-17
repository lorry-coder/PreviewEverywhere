package main

import "flag"

// parseFlags 允许标志与位置参数任意穿插。
//
// Go 标准库的 flag 包遇到第一个位置参数就停止解析，于是
//
//	pe watch add ~/项目/docs --project auth
//	pe push report.md --tag 风险
//
// 这两条最自然的写法里，后面的标志会被静默忽略——不报错，只是不生效。
// 这种坑发现得很晚（表现是「我明明指定了项目名，怎么没用上」），
// 所以这里把解析改成穿插式的。
func parseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}
