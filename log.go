package main

import (
	"fmt"
	"log"
	"os"
)

func init() {
	log.SetFlags(0)
	log.SetOutput(os.Stdout)
}

func Info(format string, v ...any) {
	log.Printf("[INFO] "+format, v...)
}

func Warn(format string, v ...any) {
	log.Printf("[WARN] "+format, v...)
}

func Fatal(format string, v ...any) {
	fmt.Fprintf(os.Stderr, "[FATAL] "+format+"\n", v...)
	os.Exit(1)
}
