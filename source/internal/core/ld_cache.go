package core

// The hour cache: the messages of closed hours, parsed, kept in memory so a search of the last few hours does not read, decompress and
// parse the files again.  Like flowdir's rollups it is capped at a percentage of the machine's memory (the container limit when that is
// smaller), 50 unless set, and the oldest hours go first.  The hour being written is never cached, and an hour that does not fit is read
// from its file as before, so the cap costs speed only.
const LdDefaultMemPct = 50
