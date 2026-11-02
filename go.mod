module github.com/pthm/leeroy

go 1.26.0

require (
	github.com/go-sql-driver/mysql v1.10.1
	github.com/goccy/go-yaml v1.19.2
	github.com/pthm/leeroy/azeroth-sdk v0.0.0
	golang.org/x/exp/eval v0.0.0-20261014143641-f0251eb4aa3e
)

require filippo.io/edwards25519 v1.2.0 // indirect

replace github.com/pthm/leeroy/azeroth-sdk => ./azeroth-sdk
