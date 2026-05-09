package room

import "errors"

var (
	ErrUserCancelled    = errors.New("user cancelled envelope")
	ErrRoomDisconnected = errors.New("room: disconnected")
	ErrRoomClosed       = errors.New("room: closed")
	ErrNoConn           = errors.New("room: no active websocket connection")
	ErrRoomNotFound     = errors.New("room: not found")
	ErrInvalidPhaseID   = errors.New("room: invalid phase id")
	ErrInvalidPhaseKey  = errors.New("room: invalid phase output key")
)
