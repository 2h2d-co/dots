package dots

func analyzeApplyStatus(rt *Runtime, scope pathScope) (statusReport, []FileRecord, []FileRecord, error) {
	inputs, err := loadStatusInputs(rt)
	if err != nil {
		return statusReport{}, nil, nil, err
	}
	initialReport, _, err := analyzeStatusFromInputs(rt, inputs)
	if err != nil {
		return statusReport{}, nil, nil, err
	}
	if err := validatePathScope(scope, inputs, initialReport); err != nil {
		return statusReport{}, nil, nil, err
	}
	if !scope.active() {
		return initialReport, inputs.Records, nil, nil
	}

	acceptedRecords, err := acceptedScopedProfileRecords(rt, initialReport, scope)
	if err != nil {
		return statusReport{}, nil, nil, err
	}
	if len(acceptedRecords) == 0 {
		return scope.filterReport(initialReport), scope.filterRecords(inputs.Records), nil, nil
	}
	inputs.Records = mergeFileRecords(inputs.Records, acceptedRecords)

	report, records, err := analyzeStatusFromInputs(rt, inputs)
	if err != nil {
		return statusReport{}, nil, nil, err
	}
	return scope.filterReport(report), scope.filterRecords(records), acceptedRecords, nil
}

func analyzeSyncStatus(rt *Runtime, scope pathScope) (statusReport, []FileRecord, error) {
	inputs, err := loadStatusInputs(rt)
	if err != nil {
		return statusReport{}, nil, err
	}
	report, records, err := analyzeStatusFromInputs(rt, inputs)
	if err != nil {
		return statusReport{}, nil, err
	}
	if err := validatePathScope(scope, inputs, report); err != nil {
		return statusReport{}, nil, err
	}
	return scope.filterReport(report), scope.filterRecords(records), nil
}

func acceptedScopedProfileRecords(rt *Runtime, report statusReport, scope pathScope) ([]FileRecord, error) {
	if !scope.active() {
		return nil, nil
	}
	accepted := make([]FileRecord, 0)
	seen := make(map[string]struct{})
	for _, item := range report.Repo {
		if !scope.contains(item.Path) {
			continue
		}
		switch item.Kind {
		case kindRepoModified, kindRepoUntracked:
			if _, ok := seen[item.Path]; ok {
				continue
			}
			record, err := fileRecord(profileDir(rt), item.Path)
			if err != nil {
				return nil, err
			}
			accepted = append(accepted, record)
			seen[item.Path] = struct{}{}
		}
	}
	sortFileRecords(accepted)
	return accepted, nil
}
