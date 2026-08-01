// Test seams: helpers only test code uses, kept out of the production binary.
package cron

func (s Schedule) String() string { return s.expr }
