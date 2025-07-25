package log

type ILogger interface {
	StartLog(param interface{}) error
	EndLog() error
	DoLog(format string, args ...interface{}) error
}
