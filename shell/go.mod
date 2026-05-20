module github.com/convergent-systems-co/aish/shell

go 1.22

require (
	github.com/BurntSushi/toml v1.6.0
	github.com/convergent-systems-co/aish/libs/proto v0.0.0
)

replace github.com/convergent-systems-co/aish/libs/proto => ../libs/proto
