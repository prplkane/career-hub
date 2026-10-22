package applications

// HasAppliedEvent deliberately checks the history, not the current status or
// the resume snapshot flag. This is the record-submission source contract.
func (d *Detail) HasAppliedEvent() bool {
	for _, event := range d.StatusHistory {
		if event.Status.value == "applied" {
			return true
		}
	}
	return false
}

func (d *Detail) HasSubmittedResume(id string) bool {
	return d.AppliedResume.Resume != nil && d.AppliedResume.Resume.ID.value == id
}
