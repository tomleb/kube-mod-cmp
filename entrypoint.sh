#!/bin/sh -l

# Add go to PATH
export PATH=/usr/local/go/bin:$PATH

if [ "$INPUT_ACTION" = "check" ]; then
	opts=""
	if [ -n "${INPUT_IGNORE_FILE}" ]; then
		opts="$opts --ignore-file ${INPUT_IGNORE_FILE}"
	fi
	if [ -n "${INPUT_K8S_VERSION}" ]; then
		opts="$opts --k8s-version ${INPUT_K8S_VERSION}"
	fi
	kube-mod-cmp check $opts
fi
