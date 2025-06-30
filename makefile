# Makefile to archive logs and output

.PHONY: archive

archive:
	@echo "Archiving logs to archive/logs_archive/"
	mkdir -p archive/logs_archive
	mv logs/* archive/logs_archive/ 2>/dev/null || echo "No files in logs/ to move."

	@echo "Archiving output to archive/output_archive/"
	mkdir -p archive/output_archive
	mv output/* archive/output_archive/ 2>/dev/null || echo "No files in output/ to move."
