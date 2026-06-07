package cli

import (
	"strings"

	"github.com/Gitlawb/zero/internal/sessions"
)

type execSessionRecorder struct {
	prepared sessions.PreparedExec
	err      error
}

func shouldUseExecSession(options execOptions) bool {
	return options.outputFormat == execOutputStreamJSON ||
		options.resume != "" ||
		options.resumeLatest ||
		options.fork != "" ||
		options.initSessionID != ""
}

func preflightExecSession(options execOptions) error {
	if options.resume == "" && !options.resumeLatest && options.fork == "" {
		return nil
	}
	if (options.resume != "" || options.resumeLatest) && options.fork != "" {
		return execUsageError{"Use either --resume or --fork, not both."}
	}

	store := sessions.NewStore(sessions.StoreOptions{})
	switch {
	case options.fork != "":
		session, err := store.Get(options.fork)
		if err != nil {
			return err
		}
		if session == nil {
			return execUsageError{"Zero session not found: " + options.fork}
		}
	case options.resume != "":
		session, err := store.Get(options.resume)
		if err != nil {
			return err
		}
		if session == nil {
			return execUsageError{"Zero session not found: " + options.resume}
		}
	case options.resumeLatest:
		latest, err := store.Latest()
		if err != nil {
			return err
		}
		if latest == nil {
			return execUsageError{"No Zero sessions available to resume."}
		}
	}
	return nil
}

func createSessionTitle(prompt string) string {
	title := strings.Join(strings.Fields(prompt), " ")
	if len(title) > 80 {
		title = title[:80]
	}
	if title == "" {
		return "Zero exec session"
	}
	return title
}

func execSessionTitle(options execOptions, prompt string) string {
	if title := strings.TrimSpace(options.sessionTitle); title != "" {
		return title
	}
	return createSessionTitle(prompt)
}

func (recorder *execSessionRecorder) append(eventType sessions.EventType, payload any) {
	if recorder.err != nil || recorder.prepared.Store == nil || recorder.prepared.Session.SessionID == "" {
		return
	}
	_, recorder.err = recorder.prepared.Store.AppendEvent(recorder.prepared.Session.SessionID, sessions.AppendEventInput{
		Type:    eventType,
		Payload: payload,
	})
}
