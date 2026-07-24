package swarm

import (
	"context"
	"fmt"
)

// Spawn registers a task and launches a member of agentType to run it under the
// given team, inheriting the orchestrator's policy. It returns the task id
// immediately; the member runs concurrently (under the Swarm's base context, so
// it outlives this call) and reports its result through the coordinator. If the
// team is at its slot cap the member is queued and launches when a slot frees
// (it stays pending in the coordinator until then).
func (s *Swarm) Spawn(pol Policy, teamName, agentType, task, cwd string) (string, error) {
	if err := s.beginLifecycleAdmission(); err != nil {
		return "", err
	}
	defer s.lifecycleMu.RUnlock()

	def, err := s.registry.Lookup(agentType)
	if err != nil {
		return "", err
	}
	team := sanitizeName(teamName)
	id := s.nextID(agentType)
	if _, err := s.coord.Register(id, id, team, task); err != nil {
		return "", err
	}
	s.rememberCwd(id, cwd)
	spec := s.buildSpec(pol, id, id, team, def, task, cwd)
	s.dispatchAdmitted(spec)
	return id, nil
}

func (s *Swarm) beginLifecycleAdmission() error {
	s.lifecycleMu.RLock()
	if s.closed {
		s.lifecycleMu.RUnlock()
		return ErrSwarmClosed
	}
	return nil
}

// dispatch admits a spec to its team (launching now or queuing for a slot).
// The caller must hold lifecycleMu for reading.
func (s *Swarm) dispatchAdmitted(spec MemberSpec) {
	t := s.team(spec.Team)
	if t.admit(spec) {
		s.launchAdmitted(t, spec)
	}
	// Otherwise the spec is queued; the coordinator task stays pending until a
	// slot frees and afterExit launches it.
}

// launch starts a member for spec and supervises it. A synchronous launch
// failure fails the task and frees the slot.
// The caller must hold lifecycleMu for reading.
func (s *Swarm) launchAdmitted(t *Team, spec MemberSpec) {
	handle, err := s.launcher.Launch(s.baseCtx, spec)
	if err != nil {
		_ = s.coord.Fail(spec.TaskID, "launch: "+err.Error())
		s.afterExitAdmitted(t)
		return
	}
	m := &Member{ID: spec.ID, AgentType: spec.AgentType, TaskID: spec.TaskID, handle: handle}
	t.addMember(m)
	_ = s.coord.SetStatus(spec.TaskID, StatusRunning)
	s.watchers.Add(1)
	go func() {
		defer s.watchers.Done()
		s.watch(t, m, spec)
	}()
}

// watch awaits a member, applies bounded relaunch on temporary failures, records
// the terminal outcome, then frees the slot and drains the queue.
//
// The relaunch counter is per-member (m.restarts) and the same spec is reused on
// retry. This is sound because a Member is bound 1:1 to its spec for its whole
// life (Member.ID == MemberSpec.ID); a retry never reuses the struct for a
// different spec.
func (s *Swarm) watch(t *Team, m *Member, spec MemberSpec) {
	for {
		res, err := m.handle.Wait()
		if err != nil {
			if isRetryable(err) && m.restarts < maxMemberRestarts {
				if s.beginLifecycleAdmission() == nil {
					nh, relErr := s.launcher.Launch(s.baseCtx, spec)
					s.lifecycleMu.RUnlock()
					if relErr == nil {
						m.restarts++
						m.handle = nh
						continue
					}
				}
				// Fall through if shutdown started or relaunch failed.
			}
			// res.SessionID is preserved by the handle even on error, so a member that
			// ran then failed stays drillable; a pure launch error carries an empty id.
			_ = s.coord.FailWithSession(m.TaskID, memberError(err), res.SessionID)
		} else {
			_ = s.coord.CompleteWithSession(m.TaskID, res.Result, res.SessionID)
		}
		break
	}
	t.removeMember(m.ID)
	s.afterExit(t)
}

// afterExit releases the just-vacated slot and launches the next queued spec, if
// any. Each exit drains at most one queued member; that member's own exit drains
// the next, so the queue empties one-per-slot without unbounded recursion.
func (s *Swarm) afterExit(t *Team) {
	if s.beginLifecycleAdmission() != nil {
		t.releaseSlot()
		return
	}
	defer s.lifecycleMu.RUnlock()
	s.afterExitAdmitted(t)
}

// afterExitAdmitted drains at most one queued spec while lifecycle admission is
// held open. The caller must hold lifecycleMu for reading.
func (s *Swarm) afterExitAdmitted(t *Team) {
	next, ok := t.onExit()
	if !ok {
		return
	}
	s.launchAdmitted(t, next)
}

// Handoff transfers a task to a fresh member of toAgentType, delivering a note to
// the new member's inbox and marking the original task handed-off. It returns the
// new task id. A handoff of an already-terminal task is rejected (fail closed).
func (s *Swarm) Handoff(pol Policy, teamName, taskID, toAgentType, note string) (string, error) {
	if err := s.beginLifecycleAdmission(); err != nil {
		return "", err
	}
	defer s.lifecycleMu.RUnlock()

	task, ok := s.coord.Get(taskID)
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownTask, taskID)
	}
	if task.Status.terminal() {
		return "", fmt.Errorf("swarm: task %s already %s; cannot hand off", taskID, task.Status)
	}
	def, err := s.registry.Lookup(toAgentType)
	if err != nil {
		return "", err
	}
	team := sanitizeName(teamName)
	newID := s.nextID(toAgentType)
	handoffTask := task.Description
	if note != "" {
		handoffTask += "\n\nHandoff note: " + note
	}
	// Deliver the handoff note to the new member's inbox BEFORE registering the new
	// task, so a mailbox failure can't leave a phantom pending task in the
	// coordinator (it returns the error having registered nothing) (M5).
	if note != "" {
		if mbErr := s.mailbox.Send(team, newID, Message{
			From: task.AgentID, Subject: "handoff", Body: note, Type: "handoff", Time: nowRFC3339(),
		}); mbErr != nil {
			return "", fmt.Errorf("swarm: deliver handoff note: %w", mbErr)
		}
	}
	if _, err := s.coord.Register(newID, newID, team, handoffTask); err != nil {
		return "", err
	}
	cwd := s.cwdFor(taskID)
	s.rememberCwd(newID, cwd)
	// Retire the original task (it has been re-delegated).
	_ = s.coord.SetStatus(taskID, StatusHandedOff)
	spec := s.buildSpec(pol, newID, newID, team, def, handoffTask, cwd)
	s.dispatchAdmitted(spec)
	return newID, nil
}

// AdoptOrphans re-parents tasks in a team whose owning member is no longer live
// (e.g. a crashed worker) onto fresh members of toAgentType, returning the
// adopted task ids. Terminal tasks and tasks with a live owner are left alone.
func (s *Swarm) AdoptOrphans(pol Policy, teamName, toAgentType string) ([]string, error) {
	if err := s.beginLifecycleAdmission(); err != nil {
		return nil, err
	}
	defer s.lifecycleMu.RUnlock()

	def, err := s.registry.Lookup(toAgentType)
	if err != nil {
		return nil, err
	}
	team := sanitizeName(teamName)
	t := s.team(team)
	live := t.liveAgents()
	var adopted []string
	for _, task := range s.coord.List() {
		if task.Team != team || task.Status.terminal() {
			continue
		}
		if _, ok := live[task.AgentID]; ok {
			continue // still has a live member
		}
		newAgent := s.nextID(toAgentType)
		if err := s.coord.Reassign(task.ID, newAgent); err != nil {
			continue // raced to terminal; skip
		}
		cwd := s.cwdFor(task.ID)
		s.rememberCwd(task.ID, cwd)
		spec := s.buildSpec(pol, newAgent, task.ID, team, def, task.Description, cwd)
		s.dispatchAdmitted(spec)
		adopted = append(adopted, task.ID)
	}
	return adopted, nil
}

// Collect returns snapshots of every task in a team, for swarm_collect/status.
func (s *Swarm) Collect(teamName string) []Task {
	team := sanitizeName(teamName)
	var out []Task
	for _, task := range s.coord.List() {
		if task.Team == team {
			out = append(out, task)
		}
	}
	return out
}

// CollectWait blocks until the team's tasks all reach a terminal state (or ctx is
// done), then returns their snapshots. This is what swarm_collect uses so the
// orchestrator gets final results in a single call instead of polling
// swarm_status repeatedly while members are still running.
func (s *Swarm) CollectWait(ctx context.Context, teamName string) []Task {
	return s.coord.WaitSettled(ctx, sanitizeName(teamName))
}
