import { afterEach, describe, expect, it } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { peekSessionSearchReveal, resetSessionSearchReveals, revealSessionSearchMatch } from "./session-search-reveal";

describe("revealSessionSearchMatch", () => {
	afterEach(() => {
		resetSessionSearchReveals();
		useUiStore.setState({ chatMessageFocusRequest: null });
	});

	it("hands a session-transcript match to the session with its matched text", () => {
		revealSessionSearchMatch({ sessionId: "s1", messageId: "m1", snippet: "the flaky test", matchKind: "user" });
		expect(peekSessionSearchReveal("s1")).toEqual({ messageId: "m1", text: "the flaky test", find: true });
		expect(useUiStore.getState().chatMessageFocusRequest).toBeNull();
	});

	// Find in chat searches user and assistant text; a word matched in tool or
	// command output, a path or the name would open it on "No results".
	it("asks for find in chat only for a match in a message's words", () => {
		for (const [matchKind, find] of [["user", true], ["assistant", true], ["tool", false], ["command", false], ["path", false], [undefined, false]] as const) {
			revealSessionSearchMatch({ sessionId: "s1", messageId: "m1", snippet: "flaky", matchKind });
			expect(peekSessionSearchReveal("s1")?.find, String(matchKind)).toBe(find);
		}
	});

	it("opens a reviewer-chat match through the reviewer chat's focus request", () => {
		revealSessionSearchMatch({ sessionId: "s1", messageId: "m9", snippet: "…flagged a regression…", reviewId: "r1" });
		// The same request the palette's chat search makes, which is what brings
		// the reviewer chat forward; the session's own reveal stays untouched.
		expect(useUiStore.getState().chatMessageFocusRequest).toMatchObject({
			sessionId: "s1", messageId: "m9", reviewId: "r1", text: "flagged a regression",
		});
		expect(peekSessionSearchReveal("s1")).toBeUndefined();
	});

	it("highlights the matched word rather than the whole snippet", () => {
		// A snippet is cut and whitespace-collapsed, so it rarely matches the
		// rendered message; the matched word does, and the timeline marks it.
		revealSessionSearchMatch({
			sessionId: "s1", messageId: "m1", snippet: "…retry backoff  never resets…",
			highlights: [{ start: 7, end: 14 }],
		});
		expect(peekSessionSearchReveal("s1")).toMatchObject({ messageId: "m1", text: "backoff" });
	});

	it("does nothing for a match with no message to open at", () => {
		revealSessionSearchMatch({ sessionId: "s1", snippet: "title match" });
		expect(peekSessionSearchReveal("s1")).toBeUndefined();
		expect(useUiStore.getState().chatMessageFocusRequest).toBeNull();
	});
});
