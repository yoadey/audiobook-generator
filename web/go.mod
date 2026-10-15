// Not a real Go module. It only exists so the root module's Go tooling
// (go list/test/vet ./...) skips web/: some npm packages (e.g. flatted) ship
// .go files inside node_modules.
module github.com/yoadey/audiobook-generator/web

go 1.27
