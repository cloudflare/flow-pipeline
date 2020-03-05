.PHONY: proto
proto:
	@echo generating protobuf
	protoc --go_out=. --plugin=$(PROTOCPATH)protoc-gen-go pb-ext/*.proto