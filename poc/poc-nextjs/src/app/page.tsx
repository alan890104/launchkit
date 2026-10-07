import { prisma } from "@/lib/db";

export const dynamic = "force-dynamic";

export default async function Home() {
  const posts = await prisma.post.findMany({
    orderBy: { createdAt: "desc" },
    take: 10,
  });

  return (
    <main className="max-w-2xl mx-auto p-8">
      <h1 className="text-3xl font-bold mb-6">POC A: Next.js + Prisma</h1>
      <p className="text-gray-500 mb-4">
        SSR page — fetches posts from PostgreSQL on every request.
      </p>

      {posts.length === 0 ? (
        <p className="text-gray-400">No posts yet. POST to /api/posts to create one.</p>
      ) : (
        <ul className="space-y-3">
          {posts.map((post) => (
            <li key={post.id} className="border rounded p-4">
              <h2 className="font-semibold">{post.title}</h2>
              {post.content && <p className="text-gray-600 mt-1">{post.content}</p>}
              <time className="text-xs text-gray-400">
                {post.createdAt.toISOString()}
              </time>
            </li>
          ))}
        </ul>
      )}
    </main>
  );
}
