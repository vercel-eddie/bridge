all: clean generate

# Generate Go and TypeScript code from protobuf definitions
generate:
	buf generate
	cd api/go && go mod tidy
	cd api/ts && npm install

# Clean generated files
clean:
	rm -rf api/go/bridge
	rm -rf api/ts/bridge
