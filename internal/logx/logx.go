// Package logx exposes shared loggers for all commands.
package logx

import (
	"log"
	"os"
)

var (
	Info  = log.New(os.Stdout, "INFO\t", log.Ldate|log.Ltime|log.Lmsgprefix)
	Error = log.New(os.Stderr, "ERROR\t", log.Ldate|log.Ltime|log.Lshortfile|log.Lmsgprefix)
)
