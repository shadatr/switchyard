import Link from "next/link";

export default function NotFound() {
  return (
    <div className="flex flex-col items-start gap-2 py-16">
      <p className="font-mono text-[12px] text-muted">404</p>
      <h1 className="text-[22px] font-semibold tracking-[-0.02em]">No such track.</h1>
      <Link href="/" className="text-[13px] text-signal underline-offset-4 hover:underline">
        Back to overview
      </Link>
    </div>
  );
}
