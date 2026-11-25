package kb

// DashboardFileOps turns the bytes of a dashboard file into the ops of one
// dashboard_patch call that creates it: set_title, set_description (each when
// non-empty) and one add per widget, in file order. Pure function of its bytes
// (no KB, no filesystem), so a plugin loader and the tool share it. A widget is
// copied through the shared struct (NewAddOp), never re-encoded by hand, and a
// widget that carries a Problem is refused: the file must be valid.
func DashboardFileOps(data []byte) (title, description string, ops []DashboardOp, err error) {
	d, err := ParseDashboard(data)
	if err != nil {
		return "", "", nil, err
	}
	ops = []DashboardOp{}
	if d.Title != "" {
		t := d.Title
		ops = append(ops, DashboardOp{Op: DashboardOpSetTitle, Title: &t})
	}
	if d.Description != "" {
		s := d.Description
		ops = append(ops, DashboardOp{Op: DashboardOpSetDescription, Description: &s})
	}
	for _, w := range d.Widgets {
		op, err := NewAddOp(w)
		if err != nil {
			return "", "", nil, err
		}
		ops = append(ops, op)
	}
	return d.Title, d.Description, ops, nil
}
