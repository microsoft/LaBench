module labench

require (
	golang.org/x/net v0.7.0
	gopkg.in/yaml.v2 v2.2.2
	labench/bench v0.0.0
)

require (
	github.com/codahale/hdrhistogram v0.0.0-20161010025455-3a0bb77429bd // indirect
	github.com/mattn/go-runewidth v0.0.4 // indirect
	github.com/olekukonko/tablewriter v0.0.1 // indirect
	golang.org/x/text v0.7.0 // indirect
)

replace labench/bench => ./bench
