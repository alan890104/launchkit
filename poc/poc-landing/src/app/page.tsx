export default function Home() {
  return (
    <main className="min-h-screen flex flex-col items-center justify-center bg-black text-white">
      <h1 className="text-5xl font-bold mb-4">LaunchKit</h1>
      <p className="text-xl text-gray-400 mb-8 max-w-lg text-center">
        The full-lifecycle cloud platform for AI developers.
        One MCP takes you from database to deploy to lower bills.
      </p>
      <div className="flex gap-4">
        <a
          href="https://github.com/launchkit"
          className="px-6 py-3 bg-white text-black rounded-full font-medium hover:bg-gray-200 transition"
        >
          Get Started
        </a>
        <a
          href="https://docs.launchkit.dev"
          className="px-6 py-3 border border-gray-600 rounded-full font-medium hover:border-white transition"
        >
          Documentation
        </a>
      </div>
    </main>
  );
}
