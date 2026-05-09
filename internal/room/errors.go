package room

import "errors"

var (
	ErrUserCancelled                      = errors.New("user cancelled envelope")
	ErrRoomDisconnected                   = errors.New("room: disconnected")
	ErrRoomClosed                         = errors.New("room: closed")
	ErrNoConn                             = errors.New("room: no active websocket connection")
	ErrRoomNotFound                       = errors.New("room: not found")
	ErrInvalidPhaseID                     = errors.New("room: invalid phase id")
	ErrInvalidPhaseKey                    = errors.New("room: invalid phase output key")
	ErrInvalidWhiteboardBoardID           = errors.New("room: invalid whiteboard board id")
	ErrInvalidWhiteboardAssetRef          = errors.New("room: invalid whiteboard asset ref")
	ErrInvalidWhiteboardRevisionID        = errors.New("room: invalid whiteboard revision id")
	ErrInvalidDraftBlockID                = errors.New("room: invalid draft block id")
	ErrInvalidDraftBlockContent           = errors.New("room: invalid draft block content")
	ErrInvalidProseRevisionID             = errors.New("room: invalid prose revision id")
	ErrInvalidProseRevisionLens           = errors.New("room: invalid prose revision lens")
	ErrInvalidProseRevisionSourceText     = errors.New("room: invalid prose revision source text")
	ErrInvalidProseRevisionSuggestionID   = errors.New("room: invalid prose revision suggestion id")
	ErrInvalidProseRevisionSuggestionText = errors.New("room: invalid prose revision suggestion text")
	ErrInvalidProseRevisionDecision       = errors.New("room: invalid prose revision decision")
	ErrInvalidProseRevisionOutcome        = errors.New("room: invalid prose revision outcome")
	ErrInvalidFinalOutputMarkdown         = errors.New("room: invalid final output markdown")
	ErrInvalidFinalOutputFormat           = errors.New("room: invalid final output format")
)
