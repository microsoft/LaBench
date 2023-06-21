module labench

go 1.17

require (
	golang.org/x/net v0.0.0-20190522155817-f3200d17e092
	gopkg.in/yaml.v2 v2.4.0
	labench/bench v0.0.0
)

require (
	github.com/codahale/hdrhistogram v0.0.0-20161010025455-3a0bb77429bd // indirect
	github.com/mattn/go-runewidth v0.0.4 // indirect
	github.com/olekukonko/tablewriter v0.0.1 // indirect
	golang.org/x/text v0.3.0 // indirect
)

replace labench/bench => ./bench
