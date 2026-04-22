#ifndef JSONL_H
#define JSONL_H
#include <stddef.h>
#include <stdint.h>

size_t jsonl_count_lines(const uint8_t *ptr, size_t len);
size_t jsonl_fill_offsets(const uint8_t *ptr, size_t len, size_t *offsets, size_t max_count);
size_t jsonl_find_newlines(const uint8_t *ptr, size_t len, size_t *positions, size_t max_count);
size_t jsonl_scan_lines(const uint8_t *ptr, size_t len, size_t *offsets, size_t max_count);

#endif
