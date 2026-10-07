import { prisma } from "@/lib/db";
import { NextRequest, NextResponse } from "next/server";

export async function GET() {
  const posts = await prisma.post.findMany({ orderBy: { createdAt: "desc" } });
  return NextResponse.json(posts);
}

export async function POST(req: NextRequest) {
  const body = await req.json();
  const post = await prisma.post.create({
    data: { title: body.title, content: body.content ?? null },
  });
  return NextResponse.json(post, { status: 201 });
}
