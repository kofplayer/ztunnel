SET CGO_ENABLED=0
SET GOARCH=amd64

SET GOOS=linux
cd client
go build
SET GOOS=windows
go build

SET GOOS=linux
cd ..\server
go build
SET GOOS=windows
go build

move server ..\ztunnel_server_linux_x64
move server.exe ..\ztunnel_server_windows_x64.exe

cd ..\client
move client ..\ztunnel_client_linux_x64
move client.exe ..\ztunnel_client_windows_x64.exe