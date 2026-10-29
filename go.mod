module github.com/pthm/leeroy

go 1.26.0

require (
	github.com/go-sql-driver/mysql v1.10.1
	github.com/pthm/leeroy/azeroth-sdk v0.0.0
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/o0olele/detour-go v0.2.3 // indirect
)

replace github.com/pthm/leeroy/azeroth-sdk => ./azeroth-sdk
