#!/bin/sh -l

if [ "$INPUT_ACTION" = "check" ]; then
	opts=""
	if [ -n "$INPUT-IGNORE-FILE" ]; then
		opts="$opts --ignore-file ${INPUT_IGNORE-FILE}"
	fi
	kube-mod-cmp check --k8s-version v1.28.6 $opts
fi
