// sensors_freebsd.go
// SPDX-License-Identifier: BSD-3-Clause
//go:build freebsd

package sensors

/*
#include <sys/types.h>
#include <sys/sysctl.h>
#include <stdlib.h>

// Помощна функция за sysctlbyname, връща стойност като C-стринг.
// ВНИМАНИЕ: буферът се заделя с malloc и трябва да се free-не в Go.
int sysctlbyname_str(const char *name, char **out, size_t *outlen) {
	size_t len = 0;
	int ret = sysctlbyname(name, NULL, &len, NULL, 0);
	if (ret != 0) {
		return ret;
	}
	char *buf = (char *)malloc(len);
	if (buf == NULL) {
		return -1;
	}
	ret = sysctlbyname(name, buf, &len, NULL, 0);
	if (ret != 0) {
		free(buf);
		return ret;
	}
	*out = buf;
	*outlen = len;
	return 0;
}
*/
import "C"

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unsafe"

	"github.com/shirou/gopsutil/v4/internal/common"
)

// parseFreeBSDTemperature парсва "70,0C", "27,9C" и подобни към float64 (°C).
func parseFreeBSDTemperature(raw string) (float64, error) {
	s := strings.TrimSpace(raw)

	// Ако идва във формат "name: value", отрязваме преди двоеточието.
	if idx := strings.LastIndex(s, ":"); idx != -1 {
		s = strings.TrimSpace(s[idx+1:])
	}

	// Махаме "C" / "°C" накрая, ако ги има.
	s = strings.TrimSuffix(s, "C")
	s = strings.TrimSuffix(s, "°")
	s = strings.TrimSpace(s)

	// Заменяме запетая с точка (FreeBSD локали).
	s = strings.ReplaceAll(s, ",", ".")

	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("parse temperature %q: %w", raw, err)
	}
	return f, nil
}

// sysctlStringByName връща sysctl стойност като Go string.
func sysctlStringByName(name string) (string, error) {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	var buf *C.char
	var length C.size_t

	ret := C.sysctlbyname_str(cname, &buf, &length)
	if ret != 0 {
		return "", fmt.Errorf("sysctlbyname(%s) failed with %d", name, int(ret))
	}
	defer C.free(unsafe.Pointer(buf))

	goBytes := C.GoBytes(unsafe.Pointer(buf), C.int(length))
	// sysctl string-ове обикновено завършват с '\0' + нов ред.
	s := string(goBytes)
	s = strings.TrimRight(s, "\x00\r\n")
	return s, nil
}

// sysctlIntByName чете цели числа (примерно hw.ncpu).
func sysctlIntByName(name string) (int, error) {
	s, err := sysctlStringByName(name)
	if err != nil {
		return 0, err
	}
	s = strings.TrimSpace(s)
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("parse int sysctl %s=%q: %w", name, s, err)
	}
	return n, nil
}

func TemperaturesWithContext(_ context.Context) ([]TemperatureStat, error) {
	var stats []TemperatureStat

	// 1) Общ ACPI thermal сензор, ако го има.
	if s, err := sysctlStringByName("hw.acpi.thermal.tz0.temperature"); err == nil {
		if t, err := parseFreeBSDTemperature(s); err == nil {
			stats = append(stats, TemperatureStat{
				SensorKey:   "hw.acpi.thermal.tz0.temperature",
				Temperature: t,
			})
		}
	}

	// 2) dev.cpu.N.temperature за N в [0, hw.ncpu).
	ncpu, err := sysctlIntByName("hw.ncpu")
	if err == nil && ncpu > 0 {
		for i := 0; i < ncpu; i++ {
			oid := fmt.Sprintf("dev.cpu.%d.temperature", i)
			s, err := sysctlStringByName(oid)
			if err != nil {
				// unknown oid (като dev.cpu.12.temperature при теб) и други грешки – просто прескачаме.
				continue
			}

			t, err := parseFreeBSDTemperature(s)
			if err != nil {
				// повреден/неочакван формат – прескачаме този сензор.
				continue
			}

			stats = append(stats, TemperatureStat{
				SensorKey:   oid,
				Temperature: t,
			})
		}
	}

	if len(stats) == 0 {
		// Запазваме семантиката на gopsutil, ако няма нищо.
		return []TemperatureStat{}, common.ErrNotImplementedError
	}

	return stats, nil
}
