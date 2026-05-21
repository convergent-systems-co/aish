module github.com/convergent-systems-co/aish/shell

go 1.25.0

require (
	github.com/BurntSushi/toml v1.6.0
	github.com/anush008/fastembed-go v1.0.0
	github.com/convergent-systems-co/aish/libs/proto v0.0.0
	github.com/creack/pty v1.1.24
	github.com/philippgille/chromem-go v0.7.0
	golang.org/x/crypto v0.51.0
	golang.org/x/sys v0.44.0
	golang.org/x/term v0.43.0
	modernc.org/sqlite v1.50.1
)

require gopkg.in/yaml.v3 v3.0.1

require (
	github.com/emirpasic/gods v1.18.1 // indirect
	github.com/mitchellh/colorstring v0.0.0-20190213212951-d06e56a500db // indirect
	github.com/rivo/uniseg v0.4.6 // indirect
	github.com/schollz/progressbar/v2 v2.15.0 // indirect
	github.com/schollz/progressbar/v3 v3.14.1 // indirect
	github.com/sugarme/regexpset v0.0.0-20200920021344-4d4ec8eaf93c // indirect
	github.com/sugarme/tokenizer v0.2.3-0.20230829214935-448e79b1ed65 // indirect
	github.com/yalue/onnxruntime_go v1.7.0 // indirect
	golang.org/x/text v0.37.0 // indirect
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/godbus/dbus/v5 v5.2.2
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	modernc.org/libc v1.72.3 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

replace github.com/convergent-systems-co/aish/libs/proto => ../libs/proto
