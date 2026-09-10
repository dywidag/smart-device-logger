package fakedev

import "time"

// Chunk is one write to the device, after waiting Gap.
//
// Chunks matter as much as their contents: the real device splits lines
// across USB reads, so a line ending can arrive in the read after the line
// it terminates. Anything that assembles lines has to survive that, and it
// only gets exercised if the fake device splits the same way.
type Chunk struct {
	Gap  time.Duration
	Data string
}

// Boot is the VoidCellular v1-4-p2 power-on transcript, taken from the web
// logger's fixture at
// II-User-Interface/src/lib/components/domain/smart-devices/monitor/transcripts/boot-and-modem.ts.
//
// Every awkward thing in it is deliberate and present in the real stream:
//
//   - CRLF on ordinary boot lines
//   - a bare LF on the mreport CSV header
//   - lone CR inside the AT exchange
//   - "MEMS........" left unterminated until the result arrives, which only
//     shows up if a partial line is flushed on idle
//   - a line split across two chunks, terminator separated from its line
//   - a 311-character +COPS answer, well past any tempting buffer size
var Boot = []Chunk{
	{Data: "Bootloader activated\r\nVoidCellular v1-4-p2\r\n"},
	{Gap: 40 * time.Millisecond, Data: "Selftest - Peripherals\r\n"},
	{Gap: 250 * time.Millisecond, Data: "MEMS........"},
	{Gap: 400 * time.Millisecond, Data: "OK\r\n"},
	{Gap: 30 * time.Millisecond, Data: "RTC_RV3129........OK\r\nFLASH_AT45........OK\r\n"},
	{Gap: 30 * time.Millisecond, Data: "MODEM_SARA_R422........OK\r\n"},
	{Gap: 60 * time.Millisecond, Data: "Void Config:\r\n  site_id=0421  mode=NORMAL\r\n  report_interval=3600\r\n"},
	// A line whose terminator lands in the next chunk.
	{Gap: 80 * time.Millisecond, Data: "MEMS Active"},
	{Data: "\r\nAlarm Trigger\r\nOvernight Delay\r\n"},
	// AT soup: lone CR, blank lines, and one very long answer.
	{Gap: 120 * time.Millisecond, Data: "AT+COPS=?\r"},
	{Gap: 900 * time.Millisecond, Data: "+COPS: (2,\"3 UK\",\"3 UK\",\"23420\",7),(1,\"O2 - UK\",\"O2 -UK\",\"23410\",7),(1,\"vodafone UK\",\"voda UK\",\"23415\",7),(1,\"EE\",\"EE\",\"23430\",7),(1,\"3 UK\",\"3 UK\",\"23420\",9),(1,\"O2 - UK\",\"O2 -UK\",\"23410\",9),(1,\"vodafone UK\",\"voda UK\",\"23415\",9),(1,\"EE\",\"EE\",\"23430\",9),(1,\"Telefonica UK Limited\",\"O2 UK\",\"23410\",7),,(0-4),(0-2)\r\n\r\nOK\r\n"},
	// mreport: note the header's bare LF among CRLF rows.
	{Gap: 150 * time.Millisecond, Data: "mreport: 12 bins, 3 events\r\nbin,count,min,max,avg\n"},
	{Gap: 20 * time.Millisecond, Data: "0,142,0.01,0.42,0.09\r\n1,57,0.02,0.55,0.11\r\n"},
	{Gap: 40 * time.Millisecond, Data: "Sleep\r\n"},
}

// Replay writes chunks to the device, honouring each gap. It stops early if
// a write fails, which is what happens once the far end has gone.
func Replay(dev *Device, chunks []Chunk) error {
	for _, c := range chunks {
		if c.Gap > 0 {
			time.Sleep(c.Gap)
		}
		if err := dev.Send(c.Data); err != nil {
			return err
		}
	}
	return nil
}
