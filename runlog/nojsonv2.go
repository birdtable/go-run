//go:build nojsonv2

package runlog

// runlog is written on encoding/json/v2, which this Go release has built in: drop the nojsonv2 tag.
var _ = runlog_needs_encoding_json_v2_so_remove_the_nojsonv2_build_tag
