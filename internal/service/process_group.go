package service

import "syscall"

func sameProcessGroup(leaderPID, candidatePID int) bool {
	if leaderPID == candidatePID {
		return true
	}
	leaderGroup, leaderErr := syscall.Getpgid(leaderPID)
	candidateGroup, candidateErr := syscall.Getpgid(candidatePID)
	return leaderErr == nil && candidateErr == nil && leaderGroup == candidateGroup
}
