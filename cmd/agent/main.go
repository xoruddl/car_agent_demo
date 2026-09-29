package main

import (
	"log/slog"
	"os"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	vin := os.Getenv("VIN")
	if vin == "" {
		vin = "KMHEV6000000001"
	}

	logger.Info("vehicle agent started", "vin", vin)
}
