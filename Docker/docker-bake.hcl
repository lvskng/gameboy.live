group "default" {
    targets =   ["build-cli", "build-cli-dev", "build-bitstream-dev", "static", "bitstream-dev"]
}

target "build-cli" {
    dockerfile = "base-cli.Dockerfile"
}

target "build-cli-dev" {
    dockerfile = "base-cli.dev.Dockerfile"
}

target "build-bitstream-dev" {
    dockerfile = "base-bitstream.dev.Dockerfile"
}

target "static" {
    contexts = {
        build = "target:build-cli"
    }
    dockerfile = "Static.Dockerfile"
}

target "bitstream-dev" {
    contexts = {
        build = "target:build-bitstream-dev"
    }
    dockerfile = "Bitstream.Dockerfile"
}