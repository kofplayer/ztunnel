package log

import (
	"errors"
	"fmt"
	"log"
	"os"
)

type FileLogger struct {
	file   *os.File
	logger *log.Logger
	//msgChannel chan string
}

func (this *FileLogger) StartLog(param interface{}) error {
	filePath, ok := param.(string)
	if !ok {
		return errors.New("param is not string")
	}
	var err error
	this.file, err = os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, os.ModeAppend|os.ModePerm) //os.Create(logPath + "/" + fileNameHead + "_" + v + ".log")
	if err != nil {
		return err
	}
	this.logger = log.New(this.file, "", 0)
	/*
		this.msgChannel = make(chan string, 1000)
		go func() {
			for {
				msg, ok := <-this.msgChannel
				if !ok {
					break
				}
				this.logger.Output(0, msg)
			}
			this.logger = nil
			this.file.Close()
		}()
	*/
	return nil
}

func (this *FileLogger) EndLog() error {
	this.logger = nil
	this.file.Close()
	//close(this.msgChannel)
	return nil
}

func (this *FileLogger) DoLog(format string, args ...interface{}) error {
	//this.msgChannel <- fmt.Sprintf(format, args...)
	this.logger.Output(0, fmt.Sprintf(format, args...))
	return nil
}
