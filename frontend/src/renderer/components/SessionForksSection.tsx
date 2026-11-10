import { InspectorSection } from "@aoagents/product-ui";
import { useNavigate } from "@tanstack/react-router";
import { GitFork } from "@/components/app-icons";
import { useTranslation } from "react-i18next";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import { cn } from "../lib/utils";
import type { WorkspaceSession } from "../types/workspace";
import { ProductExternalLink } from "./ProductExternalLink";
import { useSessionForkOptions } from "./SessionForkControls";

/**
 * The inspector's Forks section: where a fork came from, with its original's
 * pull requests as read-only links, and the sessions forked from this one.
 * Nothing renders for a session that is neither a fork nor forked.
 */
export function SessionForksSection({ session }: { session: WorkspaceSession }) {
	const options = useSessionForkOptions(session);
	const { t } = useTranslation();
	const navigate = useNavigate();
	const origin = options.data?.forkedFrom;
	const forks = options.data?.forks ?? [];
	const sourcePRs = options.data?.sourcePRs ?? [];
	if (!origin && forks.length === 0) return null;
	const open = (sessionId: string) => void navigate(sessionNavigateTarget(session.workspaceId, sessionId, session.hostId));
	return (
		<InspectorSection surface={false} title={t("sessionFork.sectionTitle")}>
			<div className="flex flex-col gap-1" data-testid="session-forks">
				{origin ? (
					<div className="overflow-hidden rounded-lg bg-settings-row px-3 py-1.5">
						<ForkRow
							label={t("sessionFork.forkedFrom", { title: origin.title })}
							onOpen={origin.sourceExists ? () => open(origin.sessionId) : undefined}
						/>
						{origin.textHandoff ? <p className="mt-0.5 pl-6 text-xs text-settings-muted">{t("sessionFork.textCopy")}</p> : null}
						{sourcePRs.length > 0 ? (
							<div className="mt-1 flex flex-wrap items-center gap-1.5 pl-6">
								<span className="text-xs text-settings-muted">{t("sessionFork.originalPRs")}</span>
								{sourcePRs.map((pr) => (
									<ProductExternalLink
										ariaLabel={t("sessionFork.originalPR", { number: pr.number })}
										className="text-xs text-settings-muted underline-offset-2 hover:underline"
										href={pr.url}
										key={pr.url}
									>
										#{pr.number}
									</ProductExternalLink>
								))}
							</div>
						) : null}
					</div>
				) : null}
				{forks.map((fork) => (
					<div className={cn("overflow-hidden rounded-lg bg-settings-row px-3 py-1.5", !fork.exists && "opacity-60")} key={fork.sessionId}>
						<ForkRow label={fork.title} onOpen={fork.exists ? () => open(fork.sessionId) : undefined} />
					</div>
				))}
			</div>
		</InspectorSection>
	);
}

function ForkRow({ label, onOpen }: { label: string; onOpen?: () => void }) {
	const body = (
		<>
			<GitFork aria-hidden="true" className="size-3.5 shrink-0 text-settings-muted" />
			<span className="min-w-0 flex-1 truncate text-sm">{label}</span>
		</>
	);
	if (!onOpen) return <div className="flex min-w-0 items-center gap-2">{body}</div>;
	return (
		<button className="flex w-full min-w-0 cursor-pointer items-center gap-2 text-left" onClick={onOpen} type="button">
			{body}
		</button>
	);
}
