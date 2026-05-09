package room

import "errors"

var (
	ErrUserCancelled    = errors.New("user cancelled envelope")
	ErrRoomDisconnected = errors.New("room: disconnected")
	ErrRoomClosed       = errors.New("room: closed")
	ErrNoConn           = errors.New("room: no active websocket connection")
)
