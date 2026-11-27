import { formatBytes, formatDuration } from "./format";

describe("formatting", () => {
  it("writes durations in the largest units that fit", () => {
    expect(formatDuration(850)).toBe("850 ms");
    expect(formatDuration(12_400)).toBe("12.4 s");
    expect(formatDuration(185_000)).toBe("3 min 05 s");
    expect(formatDuration(3_720_000)).toBe("1 h 02 min");
  });

  it("writes sizes in units of 1024", () => {
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(3482)).toBe("3.4 KB");
    expect(formatBytes(1_258_291)).toBe("1.2 MB");
    expect(formatBytes(2_684_354_560)).toBe("2.5 GB");
  });
});
