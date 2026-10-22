module nasd

go 1.25

// Mínimo para compilar: con GOTOOLCHAIN=auto, un Go más viejo se cambia solo
// a este. 1.27.3 trae los parches de seguridad de octubre de 2026.
toolchain go1.27.3

// Sin dependencias externas — ADR-0013 (P8) y ADR-0017.
// Si algún día aparece una, exige justificación en un ADR.
